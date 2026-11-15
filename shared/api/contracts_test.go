package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/protocol"
)

const (
	serverID   protocol.ID = "11111111111111111111111111111111"
	accountID  protocol.ID = "22222222222222222222222222222222"
	fileID     protocol.ID = "33333333333333333333333333333333"
	versionID  protocol.ID = "44444444444444444444444444444444"
	uploadID   protocol.ID = "55555555555555555555555555555555"
	snapshotID protocol.ID = "66666666666666666666666666666666"
)

func contractLimits() protocol.TransferLimits {
	return protocol.TransferLimits{MinChunkBytes: 4, MaxChunkBytes: 8, MaxRequestBytes: MaxControlBytes, MaxManifestBytes: MaxControlBytes, MaxChunks: 100}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertFixture[T any](t *testing.T, name string, decode func(io.Reader) (T, error)) T {
	t.Helper()
	wire := fixture(t, name)
	got, err := decode(bytes.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil || !bytes.Equal(encoded, wire) {
		t.Fatalf("%s wire changed: %s, %v", name, encoded, err)
	}
	for _, body := range [][]byte{nil, []byte("null"), []byte("{}"), []byte("[]"), append(append([]byte{}, wire...), []byte("{}")...)} {
		value, err := decode(bytes.NewReader(body))
		var zero T
		if err == nil || !reflect.DeepEqual(value, zero) {
			t.Fatalf("%s accepted malformed/missing fields: %s", name, body)
		}
	}
	if _, err := decode(failedReader{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("%s swallowed read failure: %v", name, err)
	}
	return got
}

func TestContractSerializationFixtures(t *testing.T) {
	limits := contractLimits()
	for _, name := range []string{"manifest-standard", "manifest-empty", "manifest-private"} {
		t.Run(name, func(t *testing.T) {
			assertFixture(t, name, func(r io.Reader) (protocol.Manifest, error) { return DecodeManifest(r, limits, []string{"test-only"}) })
		})
	}
	u := assertFixture(t, "upload", func(r io.Reader) (Upload, error) { return DecodeUpload(r, limits) })
	assertFixture(t, "create-upload", func(r io.Reader) (CreateUploadRequest, error) { return DecodeCreateUpload(r, limits) })
	assertFixture(t, "missing-chunks", DecodeMissingChunks)
	assertFixture(t, "chunk-ack", DecodeChunkAcknowledgement)
	assertFixture(t, "finalize-result", DecodeFinalizeResult)
	assertFixture(t, "event", DecodeEvent)
	challenge := assertFixture(t, "identity-challenge", DecodeIdentityChallenge)
	proof := assertFixture(t, "identity-proof", DecodeIdentityProof)
	if err := proof.Verify(challenge, proof.TrustedIdentity, []byte("test TLS leaf DER"), make([]byte, 32), time.Unix(1800000000, 0)); err != nil {
		t.Fatal(err)
	}
	assertFixture(t, "catalog", func(r io.Reader) (CatalogPage, error) { return DecodeCatalogPage(r, nil) })
	assertFixture(t, "versions", DecodeVersionPage)
	assertFixture(t, "create-directory", func(r io.Reader) (CreateDirectoryRequest, error) { return DecodeCreateDirectory(r, nil) })
	assertFixture(t, "catalog-changes", func(r io.Reader) (CatalogChanges, error) { return DecodeCatalogChanges(r, nil) })
	if err := ValidateManifestBodyLength(int64(len(fixture(t, "manifest-standard"))), u); err != nil {
		t.Fatal(err)
	}
}

func TestContractDecoderBoundsAndAdditiveCompatibility(t *testing.T) {
	limits := contractLimits()
	decoders := []struct {
		name   string
		limit  int
		decode func(io.Reader) error
	}{
		{"create-upload", MaxControlBytes, func(r io.Reader) error { _, e := DecodeCreateUpload(r, limits); return e }},
		{"upload", MaxControlBytes, func(r io.Reader) error { _, e := DecodeUpload(r, limits); return e }},
		{"manifest-standard", MaxControlBytes, func(r io.Reader) error { _, e := DecodeManifest(r, limits, nil); return e }},
		{"missing-chunks", MaxControlBytes, func(r io.Reader) error { _, e := DecodeMissingChunks(r); return e }},
		{"chunk-ack", MaxEventBytes, func(r io.Reader) error { _, e := DecodeChunkAcknowledgement(r); return e }},
		{"finalize-result", MaxEventBytes, func(r io.Reader) error { _, e := DecodeFinalizeResult(r); return e }},
		{"catalog", MaxControlBytes, func(r io.Reader) error { _, e := DecodeCatalogPage(r, nil); return e }},
		{"event", MaxEventBytes, func(r io.Reader) error { _, e := DecodeEvent(r); return e }},
		{"identity-challenge", MaxEventBytes, func(r io.Reader) error { _, e := DecodeIdentityChallenge(r); return e }},
		{"identity-proof", MaxEventBytes, func(r io.Reader) error { _, e := DecodeIdentityProof(r); return e }},
		{"versions", MaxControlBytes, func(r io.Reader) error { _, e := DecodeVersionPage(r); return e }},
		{"create-directory", MaxControlBytes, func(r io.Reader) error { _, e := DecodeCreateDirectory(r, nil); return e }},
		{"catalog-changes", MaxControlBytes, func(r io.Reader) error { _, e := DecodeCatalogChanges(r, nil); return e }},
	}
	for _, tc := range decoders {
		t.Run(tc.name, func(t *testing.T) {
			body := fixture(t, tc.name)
			additive := append([]byte(`{"future":{"enabled":true},`), body[1:]...)
			if err := tc.decode(bytes.NewReader(additive)); err != nil {
				t.Fatalf("additive field rejected: %v", err)
			}
			padded := append(append([]byte{}, body...), bytes.Repeat([]byte(" "), tc.limit-len(body))...)
			if err := tc.decode(bytes.NewReader(padded)); err != nil {
				t.Fatalf("exact bound rejected: %v", err)
			}
			reader := bytes.NewReader(append(padded, []byte("  unread bytes")...))
			if err := tc.decode(reader); !errors.Is(err, ErrResponseTooLarge) || reader.Len() == 0 {
				t.Fatalf("oversize was not bounded: %v, unread=%d", err, reader.Len())
			}
		})
	}
	limits.MaxManifestBytes = 383
	if _, err := DecodeManifest(bytes.NewReader(fixture(t, "manifest-standard")), limits, nil); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatal("negotiated manifest bound ignored")
	}
}

func TestCreateUploadLengthAndVersionValidation(t *testing.T) {
	base := CreateUploadRequest{FileID: fileID, VersionID: versionID, Mode: protocol.Standard, ExpectedHead: "0", ChunkCount: 2, TransmittedBytes: 9, ManifestBytes: 384}
	for name, change := range map[string]func(*CreateUploadRequest){
		"file": func(u *CreateUploadRequest) { u.FileID = "" }, "version": func(u *CreateUploadRequest) { u.VersionID = "" },
		"parent": func(u *CreateUploadRequest) { u.ParentID = ".." }, "mode": func(u *CreateUploadRequest) { u.Mode = "future" },
		"revision": func(u *CreateUploadRequest) { u.ExpectedHead = "" }, "manifest bytes": func(u *CreateUploadRequest) { u.ManifestBytes = 0 },
		"negative count": func(u *CreateUploadRequest) { u.ChunkCount = -1 }, "excess chunks": func(u *CreateUploadRequest) { u.ChunkCount = 101 },
		"negative bytes": func(u *CreateUploadRequest) { u.TransmittedBytes = -1 }, "missing objects": func(u *CreateUploadRequest) { u.ChunkCount = 0 },
		"short nonfinal": func(u *CreateUploadRequest) { u.TransmittedBytes = 4 }, "too long": func(u *CreateUploadRequest) { u.TransmittedBytes = 17 },
	} {
		t.Run(name, func(t *testing.T) {
			u := base
			change(&u)
			if u.Validate(contractLimits()) == nil {
				t.Fatal("invalid upload intent accepted")
			}
		})
	}
	for _, pair := range [][2]int64{{0, 0}, {1, 1}, {1, 8}, {2, 5}, {2, 16}} {
		u := base
		u.ChunkCount, u.TransmittedBytes = pair[0], pair[1]
		if err := u.Validate(contractLimits()); err != nil {
			t.Fatalf("valid lengths %v: %v", pair, err)
		}
	}
	u := base
	u.ChunkCount, u.TransmittedBytes = math.MaxInt64, math.MaxInt64
	limits := protocol.TransferLimits{MinChunkBytes: 1, MaxChunkBytes: math.MaxInt64, MaxRequestBytes: math.MaxInt64, MaxManifestBytes: math.MaxInt64, MaxChunks: math.MaxInt64}
	if err := u.Validate(limits); err != nil {
		t.Fatalf("valid integer boundary overflowed: %v", err)
	}
}

func TestZeroDefaultsAndNegotiatedRequestCeiling(t *testing.T) {
	limits := contractLimits()
	body := strings.Replace(string(fixture(t, "manifest-empty")), `"total_bytes":0,`, "", 1)
	if _, err := DecodeManifest(strings.NewReader(body), limits, nil); err != nil {
		t.Fatalf("zero length default rejected: %v", err)
	}
	body = strings.Replace(string(fixture(t, "manifest-standard")), `"index":0,`, `"index":null,`, 1)
	if _, err := DecodeManifest(strings.NewReader(body), limits, nil); err != nil {
		t.Fatalf("zero index default rejected: %v", err)
	}
	limits.MaxRequestBytes, limits.MaxChunkBytes, limits.MaxManifestBytes = 16, 8, 16
	if _, err := DecodeCreateUpload(bytes.NewReader(fixture(t, "create-upload")), limits); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("request ceiling ignored: %v", err)
	}
	if _, err := DecodeCreateUpload(bytes.NewReader(fixture(t, "create-upload")), protocol.TransferLimits{}); err == nil {
		t.Fatal("invalid request limits accepted")
	}
}

func TestFinalizeRejectsForeignScopeAndChangedIntent(t *testing.T) {
	limits := contractLimits()
	u, err := DecodeUpload(bytes.NewReader(fixture(t, "upload")), limits)
	if err != nil {
		t.Fatal(err)
	}
	m, err := DecodeManifest(bytes.NewReader(fixture(t, "manifest-standard")), limits, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := protocol.Scope{ServerID: serverID, AccountID: accountID}
	if err := ValidateFinalize(m, u, scope, limits, nil); err != nil {
		t.Fatal(err)
	}
	for _, other := range []protocol.Scope{{ServerID: accountID, AccountID: accountID}, {ServerID: serverID, AccountID: serverID}, {}} {
		if ValidateFinalize(m, u, other, limits, nil) == nil {
			t.Fatal("foreign/invalid account scope accepted")
		}
	}
	for name, change := range map[string]func(*Upload){
		"file": func(u *Upload) { u.FileID = serverID }, "version": func(u *Upload) { u.VersionID = serverID },
		"mode": func(u *Upload) { u.Mode = protocol.Private }, "count": func(u *Upload) { u.ChunkCount = 2; u.TransmittedBytes = 9 },
		"length": func(u *Upload) { u.TransmittedBytes = 4 }, "terminal": func(u *Upload) { u.State = protocol.UploadCancelled },
		"expiry": func(u *Upload) { u.ExpiresAt = time.Time{} }, "id": func(u *Upload) { u.ID = "" }, "state": func(u *Upload) { u.State = "future" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := u
			change(&changed)
			if ValidateFinalize(m, changed, scope, limits, nil) == nil {
				t.Fatal("changed intent accepted")
			}
		})
	}
	if ValidateManifestBodyLength(u.ManifestBytes+1, u) == nil || ValidateManifestBodyLength(0, u) == nil {
		t.Fatal("changed manifest length accepted")
	}
	r := FinalizeResult{UploadID: uploadID, FileID: fileID, VersionID: versionID, State: protocol.UploadConflicted, CatalogRevision: "2", HeadRevision: "1"}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	r.State = protocol.UploadFinalizing
	if r.Validate() == nil {
		t.Fatal("incomplete finalization result accepted")
	}
}

func TestPaginationAndMissingChunkRanges(t *testing.T) {
	for _, p := range []PageRequest{{1, ""}, {1000, "YQ"}} {
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []PageRequest{{0, ""}, {-1, ""}, {1001, ""}, {1, "../x"}, {1, "YQ=="}, {1, "YR"}, {1, Cursor(strings.Repeat("a", MaxCursorBytes+1))}} {
		if p.Validate() == nil {
			t.Fatal("invalid page request accepted")
		}
	}
	base := MissingChunks{Page: Page{Snapshot: snapshotID}, UploadID: uploadID, ChunkCount: 5, Missing: []ChunkRange{{0, 2}, {4, 5}}}
	for _, ranges := range [][]ChunkRange{nil, {{-1, 2}}, {{0, 0}}, {{1, 0}}, {{0, 6}}, {{0, 2}, {1, 3}}, {{0, 2}, {2, 4}}, {{4, 5}, {0, 2}}} {
		m := base
		m.Missing = ranges
		if m.Validate() == nil {
			t.Fatalf("invalid missing ranges accepted: %+v", ranges)
		}
	}
	base.Missing = []ChunkRange{}
	base.ChunkCount = 0
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	base.Snapshot = ""
	if base.Validate() == nil {
		t.Fatal("unscoped snapshot accepted")
	}
}

func TestCatalogRevisionsAndPrivateMetadata(t *testing.T) {
	p, err := DecodeCatalogPage(bytes.NewReader(fixture(t, "catalog")), nil)
	if err != nil {
		t.Fatal(err)
	}
	entry := p.Entries[0]
	entry.Kind = Directory
	entry.HeadVersionID = ""
	if err := entry.Validate(nil); err != nil {
		t.Fatal(err)
	}
	entry.Mode = protocol.Private
	entry.Standard = nil
	entry.Private = &protocol.PrivateEnvelope{Format: "test-only", Payload: []byte("opaque")}
	if entry.Validate(nil) == nil {
		t.Fatal("unreviewed private metadata accepted")
	}
	if err := entry.Validate([]string{"test-only"}); err != nil {
		t.Fatal(err)
	}
	entry.Standard = &protocol.StandardMetadata{Name: "private plaintext"}
	if entry.Validate([]string{"test-only"}) == nil {
		t.Fatal("mixed private/standard entry accepted")
	}
	for name, change := range map[string]func(*CatalogEntry){
		"self parent": func(e *CatalogEntry) { e.ParentID = e.ID }, "bad parent": func(e *CatalogEntry) { e.ParentID = ".." },
		"zero revision": func(e *CatalogEntry) { e.HeadRevision = "0" }, "numeric revision": func(e *CatalogEntry) { e.HeadRevision = "01" },
		"unknown kind": func(e *CatalogEntry) { e.Kind = "future" }, "missing version": func(e *CatalogEntry) { e.HeadVersionID = "" },
		"directory version": func(e *CatalogEntry) { e.Kind = Directory }, "bad mode": func(e *CatalogEntry) { e.Mode = "future" },
		"missing metadata": func(e *CatalogEntry) { e.Standard = nil },
	} {
		t.Run(name, func(t *testing.T) {
			e := p.Entries[0]
			change(&e)
			if e.Validate(nil) == nil {
				t.Fatal("invalid entry accepted")
			}
		})
	}
	p.Entries = append(p.Entries, p.Entries[0])
	if p.Validate(nil) == nil {
		t.Fatal("duplicate entries accepted")
	}
	if CheckRevision("9007199254740993", "9007199254740993") != nil {
		t.Fatal("precise revision changed")
	}
	if err := CheckRevision("1", "2"); err == nil {
		t.Fatal("lost update accepted")
	} else {
		var conflict Error
		if !errors.As(err, &conflict) || conflict.Code != Conflict {
			t.Fatalf("wrong conflict: %v", err)
		}
	}
	if CheckRevision("01", "1") == nil {
		t.Fatal("noncanonical CAS accepted")
	}
}

func TestEventCompatibilityAndRequiredRevision(t *testing.T) {
	for _, e := range []Event{{Sequence: "1", Type: SessionRevoked}, {Sequence: "2", Type: StatisticsChanged}, {Sequence: "3", Type: "future"}} {
		if err := e.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []Event{{}, {Sequence: "0", Type: CatalogChanged, CatalogRevision: "1"}, {Sequence: "1", Type: CatalogChanged}, {Sequence: "1", Type: CatalogChanged, CatalogRevision: "01"}, {Sequence: "1", Type: " "}} {
		if e.Validate() == nil {
			t.Fatal("invalid event accepted")
		}
	}
}

func TestAmbiguousJSONRejected(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"conflict","code":"unavailable","message":"safe"}}`,
		`{"error":{"code":"conflict","CODE":"unavailable","message":"safe"}}`,
		`{"error":{"code":"conflict","message":"safe","me\u017f\u017fage":"ambiguous"}}`,
		`{"error":{"code":"conflict","message":"safe"},"future":{"x":1,"x":2}}`,
		`{"error":{"code":"conflict","message":"` + string([]byte{255}) + `"}}`,
		`{"future":` + strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66) + `,"error":{"code":"conflict","message":"safe"}}`,
	} {
		if _, err := DecodeError(strings.NewReader(body)); err == nil {
			t.Fatal("ambiguous/oversized nesting accepted")
		}
	}
}

func TestFormatCapabilitiesAndErrorStatusMapping(t *testing.T) {
	c, err := DecodeCapabilities(strings.NewReader(capabilitiesJSON))
	if err != nil {
		t.Fatal(err)
	}
	if c.SupportsManifestVersion(1) || c.SupportsPrivateFormat("test-only") {
		t.Fatal("missing capabilities inferred")
	}
	c.ManifestVersions = []uint16{1, 2}
	c.PrivateFormats = []string{"test-only"}
	if err := c.Validate(); err != nil || !c.SupportsManifestVersion(1) || c.SupportsManifestVersion(3) || !c.SupportsPrivateFormat("test-only") || c.SupportsPrivateFormat("other") {
		t.Fatalf("format capabilities: %+v, %v", c, err)
	}
	for _, versions := range [][]uint16{{0}, {1, 1}, nil} {
		bad := c
		bad.ManifestVersions = versions
		if bad.Validate() == nil {
			t.Fatal("invalid schema advertisement accepted")
		}
	}
	for _, formats := range [][]string{{""}, {"test-only", "test-only"}, {"UpperCase"}, {strings.Repeat("a", 65)}} {
		bad := c
		bad.PrivateFormats = formats
		if bad.Validate() == nil {
			t.Fatal("invalid format advertisement accepted")
		}
	}
	for code, status := range map[ErrorCode]int{InvalidRequest: 400, AuthenticationRequired: 401, PermissionDenied: 403, NotFound: 404, Conflict: 409, SnapshotExpired: 410, UploadGone: 410, RequestTooLarge: 413, SourceMissing: 422, Overloaded: 429, Unavailable: 503, CapacityExhausted: 507, UnsupportedVersion: 400, UnsupportedFormat: 400, InternalError: 500, "future": 500} {
		if code.HTTPStatus() != status {
			t.Fatalf("%s status=%d want %d", code, code.HTTPStatus(), status)
		}
	}
}

func FuzzContractDecoders(f *testing.F) {
	for _, name := range []string{"create-upload", "manifest-standard", "manifest-private", "catalog", "event", "identity-proof"} {
		b, err := os.ReadFile("testdata/" + name + ".json")
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte(`{"future":{"x":1,"x":2}}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		limits := contractLimits()
		_, _ = DecodeManifest(bytes.NewReader(b), limits, nil)
		_, _ = DecodeCreateUpload(bytes.NewReader(b), limits)
		_, _ = DecodeCatalogPage(bytes.NewReader(b), nil)
		_, _ = DecodeIdentityProof(bytes.NewReader(b))
		_, _ = DecodeEvent(bytes.NewReader(b))
	})
}
