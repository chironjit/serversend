package protocol

import (
	"math"
	"strings"
	"testing"
)

func standardManifest() Manifest {
	return Manifest{SchemaVersion: 1, Scope: Scope{testID, testID}, FileID: testID, VersionID: testID, Mode: Standard, TotalBytes: 9, Chunks: []Chunk{{0, 8, Digest(strings.Repeat("a", 64))}, {1, 1, Digest(strings.Repeat("b", 64))}}, Standard: &StandardMetadata{Name: "résumé.txt"}}
}

func TestManifestStandardPrivateAndEmpty(t *testing.T) {
	m := standardManifest()
	limits := testLimits()
	if err := m.Validate(limits, nil); err != nil {
		t.Fatal(err)
	}
	m.Chunks, m.TotalBytes = []Chunk{}, 0
	if err := m.Validate(limits, nil); err != nil {
		t.Fatal(err)
	}
	m.Mode, m.Standard, m.Private = Private, nil, &PrivateEnvelope{Format: "test-only", Payload: []byte("opaque test envelope")}
	if m.Validate(limits, nil) == nil {
		t.Fatal("unreviewed private format enabled")
	}
	if err := m.Validate(limits, []string{"test-only"}); err != nil {
		t.Fatal(err)
	}
}

func TestManifestRejectsMalformedAndAmbiguousContent(t *testing.T) {
	for name, change := range map[string]func(*Manifest){
		"schema": func(m *Manifest) { m.SchemaVersion = 2 },
		"server": func(m *Manifest) { m.ServerID = "" }, "account": func(m *Manifest) { m.AccountID = "" },
		"file": func(m *Manifest) { m.FileID = "../x" }, "version": func(m *Manifest) { m.VersionID = "" },
		"mode": func(m *Manifest) { m.Mode = "future" }, "missing chunks": func(m *Manifest) { m.Chunks = nil },
		"length": func(m *Manifest) { m.TotalBytes++ }, "negative total": func(m *Manifest) { m.TotalBytes = -1 },
		"negative index": func(m *Manifest) { m.Chunks[0].Index = -1 }, "duplicate index": func(m *Manifest) { m.Chunks[1].Index = 0 },
		"out of order": func(m *Manifest) { m.Chunks[0], m.Chunks[1] = m.Chunks[1], m.Chunks[0] },
		"checksum":     func(m *Manifest) { m.Chunks[0].SHA256 = "bad" }, "zero bytes": func(m *Manifest) { m.Chunks[1].Bytes = 0 },
		"short nonfinal":   func(m *Manifest) { m.Chunks[0].Bytes = 3; m.TotalBytes = 4 },
		"oversized chunk":  func(m *Manifest) { m.Chunks[0].Bytes = 9; m.TotalBytes = 10 },
		"missing metadata": func(m *Manifest) { m.Standard = nil }, "path": func(m *Manifest) { m.Standard.Name = "../x" },
		"mixed mode": func(m *Manifest) { m.Private = &PrivateEnvelope{Format: "test-only", Payload: []byte{1}} },
		"private cleartext": func(m *Manifest) {
			m.Mode = Private
			m.Private = &PrivateEnvelope{Format: "test-only", Payload: []byte{1}}
		},
		"private missing envelope": func(m *Manifest) { m.Mode = Private; m.Standard = nil },
		"private empty envelope": func(m *Manifest) {
			m.Mode = Private
			m.Standard = nil
			m.Private = &PrivateEnvelope{Format: "test-only"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := standardManifest()
			change(&m)
			if m.Validate(testLimits(), []string{"test-only"}) == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	m := standardManifest()
	if m.Validate(TransferLimits{}, nil) == nil {
		t.Fatal("invalid negotiated limits accepted")
	}
	limits := testLimits()
	limits.MaxChunks = 1
	if m.Validate(limits, nil) == nil {
		t.Fatal("excess chunks accepted")
	}
	limits = TransferLimits{1, math.MaxInt64, math.MaxInt64, math.MaxInt64, 2}
	m.Chunks[0].Bytes = math.MaxInt64
	m.TotalBytes = math.MaxInt64
	if m.Validate(limits, nil) == nil {
		t.Fatal("overflowing total accepted")
	}
}

func TestLogicalNames(t *testing.T) {
	for _, name := range []string{"report.txt", "日本語", strings.Repeat("a", 255)} {
		if err := ValidateName(name); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"", ".", "..", "a/b", "a\\b", "a\x00b", "a\nb", "a\x7fb", strings.Repeat("a", 256), string([]byte{255})} {
		if ValidateName(name) == nil {
			t.Fatalf("unsafe name accepted: %q", name)
		}
	}
}
