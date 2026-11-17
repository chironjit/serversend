package uploads

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

func batchFile(t *testing.T, name string, data []byte) api.BatchUploadFile {
	t.Helper()
	m := protocol.Manifest{SchemaVersion: protocol.ManifestVersion1, Scope: protocol.Scope{ServerID: serverID, AccountID: owner}, FileID: newID(), VersionID: newID(),
		Mode: protocol.Standard, Chunks: []protocol.Chunk{}, TotalBytes: int64(len(data)), Standard: &protocol.StandardMetadata{Name: name}}
	if len(data) > 0 {
		m.Chunks = append(m.Chunks, protocol.Chunk{Bytes: int64(len(data)), SHA256: digest(data)})
	}
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return api.BatchUploadFile{IdempotencyKey: string(newID()), Manifest: body, CreateUploadRequest: api.CreateUploadRequest{FileID: m.FileID, VersionID: m.VersionID,
		Mode: protocol.Standard, ExpectedHead: "0", ChunkCount: int64(len(m.Chunks)), TransmittedBytes: m.TotalBytes, ManifestBytes: int64(len(body))}}
}

// One corrupted file is refused alone; a replayed batch returns the recorded
// results without storing anything twice.
func TestBatchRefusesPerFileAndReplaysIdempotently(t *testing.T) {
	s, db, data := fixture(t, StoragePolicy{})
	ctx := context.Background()
	a, b := []byte("first"), []byte("second")
	request := api.BatchUploadRequest{Files: []api.BatchUploadFile{batchFile(t, "a", a), batchFile(t, "bad", []byte("expected")), batchFile(t, "empty", nil), batchFile(t, "b", b)}}
	body := func() *bytes.Reader { return bytes.NewReader(bytes.Join([][]byte{a, []byte("tampered"), b}, nil)) }
	first, err := s.UploadBatch(ctx, owner, request, body())
	if err != nil {
		t.Fatal(err)
	}
	if first[1].Error == nil || first[1].Error.Code != api.InvalidRequest {
		t.Fatalf("tampered file: %+v", first[1])
	}
	for _, i := range []int{0, 2, 3} {
		if first[i].Result == nil || first[i].Result.State != protocol.UploadCommitted {
			t.Fatalf("file %d: %+v", i, first[i])
		}
	}
	again, err := s.UploadBatch(ctx, owner, request, body())
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 2, 3} {
		if *again[i].Result != *first[i].Result {
			t.Fatalf("replay %d: %+v != %+v", i, again[i].Result, first[i].Result)
		}
	}
	var files, chunks int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM files),(SELECT count(*) FROM chunks)`).Scan(&files, &chunks); err != nil || files != 3 || chunks != 2 {
		t.Fatalf("files=%d chunks=%d %v", files, chunks, err)
	}
	visible := 0
	walkObjects(t, filepath.Join(data, "test"), func() { visible++ })
	if visible != 3 {
		t.Fatalf("%d files browsable; replays and refusals must add none", visible)
	}
	assertNoObjects(t, data)
}

// Chunks may arrive in any order; each lands at its fixed offset and the
// finished file appears at its real path.
func TestChunksArriveInAnyOrderAndAssembleInTree(t *testing.T) {
	s, _, data := fixture(t, StoragePolicy{})
	ctx := context.Background()
	size := int64(1 << 20)
	contents := bytes.Repeat([]byte("abcdefgh"), int(2*size/8))
	contents = append(contents, []byte("tail!")...)
	parts := [][]byte{contents[:size], contents[size : 2*size], contents[2*size:]}
	m := protocol.Manifest{SchemaVersion: protocol.ManifestVersion1, Scope: protocol.Scope{ServerID: serverID, AccountID: owner}, FileID: newID(), VersionID: newID(),
		Mode: protocol.Standard, TotalBytes: int64(len(contents)), Standard: &protocol.StandardMetadata{Name: "large.bin"}}
	for i, part := range parts {
		m.Chunks = append(m.Chunks, protocol.Chunk{Index: int64(i), Bytes: int64(len(part)), SHA256: digest(part)})
	}
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	u, _, err := s.Create(ctx, owner, string(newID()), api.CreateUploadRequest{FileID: m.FileID, VersionID: m.VersionID, Mode: protocol.Standard, ExpectedHead: "0",
		ChunkCount: 3, TransmittedBytes: m.TotalBytes, ManifestBytes: int64(len(body))})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{2, 1, 0} {
		if _, _, err := s.PutChunk(ctx, owner, u.ID, int64(i), int64(len(parts[i])), digest(parts[i]), bytes.NewReader(parts[i])); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
	}
	if _, err := s.Finalize(ctx, owner, u.ID, body); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(data, "test", "large.bin")); err != nil || !bytes.Equal(got, contents) {
		t.Fatalf("assembled file differs: %v", err)
	}
}

func TestNonFinalChunksMustShareOneSize(t *testing.T) {
	s, _, _ := fixture(t, StoragePolicy{})
	ctx := context.Background()
	size := int64(1 << 20)
	u, _, err := s.Create(ctx, owner, string(newID()), api.CreateUploadRequest{FileID: newID(), VersionID: newID(), Mode: protocol.Standard, ExpectedHead: "0",
		ChunkCount: 3, TransmittedBytes: 3*size + 10, ManifestBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	first := bytes.Repeat([]byte{1}, int(size+5))
	if _, _, err := s.PutChunk(ctx, owner, u.ID, 0, int64(len(first)), digest(first), bytes.NewReader(first)); err != nil {
		t.Fatal(err)
	}
	other := bytes.Repeat([]byte{2}, int(size))
	_, _, err = s.PutChunk(ctx, owner, u.ID, 1, int64(len(other)), digest(other), bytes.NewReader(other))
	code(t, err, api.InvalidRequest)
	last := bytes.Repeat([]byte{3}, 11)
	_, _, err = s.PutChunk(ctx, owner, u.ID, 2, int64(len(last)), digest(last), bytes.NewReader(last))
	code(t, err, api.InvalidRequest)
}
