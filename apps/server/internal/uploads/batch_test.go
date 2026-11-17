package uploads

import (
	"bytes"
	"context"
	"encoding/json"
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
	objects := 0
	walkObjects(t, data, func() { objects++ })
	if objects != 2 {
		t.Fatalf("%d objects stored; replays and refusals must leave none", objects)
	}
}
