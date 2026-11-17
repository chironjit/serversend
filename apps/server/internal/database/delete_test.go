package database

import (
	"strings"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

func TestDeleteTreeCancelsOpenUploads(t *testing.T) {
	db, _ := openDatabase(t)
	mustAccount(t, db, ownerA)
	mustAccount(t, db, ownerB)
	repo := NewRepository(db)
	folder := api.CreateDirectoryRequest{ID: uploadID, Mode: protocol.Standard, ExpectedHead: "0", Standard: &protocol.StandardMetadata{Name: "Folder"}}
	if _, err := repo.CreateDirectory(t.Context(), ownerA, folder); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteEntry(t.Context(), ownerB, uploadID); err == nil {
		t.Fatal("foreign deletion accepted")
	}
	for _, query := range []string{
		`INSERT INTO uploads(account_id,id,state,mode,idempotency_key,expected_bytes,created_at,parent_id,expires_at,chunk_count) VALUES(?1,?2,'open','standard','pending',4,0,?3,32503680000000,1)`,
		`INSERT INTO reservations(account_id,upload_id,bytes) VALUES(?1,?2,4)`,
		`INSERT INTO chunks(account_id,upload_id,chunk_index,byte_count,digest,byte_offset) VALUES(?1,?2,0,4,'` + strings.Repeat("d", 64) + `',0)`,
	} {
		if _, err := db.Exec(query, ownerA, ownerB, uploadID); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.DeleteEntry(t.Context(), ownerA, uploadID); err != nil {
		t.Fatalf("open upload blocked deletion: %v", err)
	}
	ops, _, err := repo.Journal(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var removesUpload, trashesFolder bool
	for _, op := range ops {
		removesUpload = removesUpload || op.Kind == "remove" && op.Target == ".serversend/uploads/"+string(ownerB)
		trashesFolder = trashesFolder || op.Kind == "move" && op.Source == string(ownerA)+"/Folder"
	}
	if !removesUpload || !trashesFolder {
		t.Fatalf("journal %+v", ops)
	}
	var remaining int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM uploads)+(SELECT count(*) FROM reservations)+(SELECT count(*) FROM chunks)+(SELECT count(*) FROM directories WHERE account_id=?)`, ownerA).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("remaining rows %d %v", remaining, err)
	}
	if _, _, err := repo.RecordChunk(t.Context(), ownerA, ownerB, StoredChunk{Index: 0, Bytes: 4, Digest: protocol.Digest(strings.Repeat("d", 64))}, time.Now()); err != ErrUploadClosed {
		t.Fatalf("late chunk after deletion: %v", err)
	}
}

func TestDeleteTreeAllowsExpiredUpload(t *testing.T) {
	db, _ := openDatabase(t)
	mustAccount(t, db, ownerA)
	repo := NewRepository(db)
	folder := api.CreateDirectoryRequest{ID: uploadID, Mode: protocol.Standard, ExpectedHead: "0", Standard: &protocol.StandardMetadata{Name: "Interrupted"}}
	if _, err := repo.CreateDirectory(t.Context(), ownerA, folder); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO uploads(account_id,id,state,mode,idempotency_key,expected_bytes,created_at,parent_id,expires_at) VALUES(?,?,'open','standard','expired',0,0,?,1)`, ownerA, ownerB, uploadID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteEntry(t.Context(), ownerA, uploadID); err != nil {
		t.Fatalf("expired upload blocked deletion: %v", err)
	}
	var remaining int
	if err := db.QueryRow(`SELECT count(*) FROM uploads WHERE account_id=?`, ownerA).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("orphaned upload: %d %v", remaining, err)
	}
}

// Large trees force the deletion TEMP table to spill beyond SQLite's cache.
// Also run this test in the read-only image with only config/data/staging
// writable: SQLITE_TMPDIR must point at staging, not the read-only /tmp.
func TestDeleteLargeTree(t *testing.T) {
	db, _ := openDatabase(t)
	// Go may need TMPDIR to create the fixture in a restricted container.
	// Clear it afterwards so SQLite must use SQLITE_TMPDIR or its OS defaults.
	t.Setenv("TMPDIR", "")
	mustAccount(t, db, ownerA)
	mustAccount(t, db, ownerB)
	repo := NewRepository(db)
	folder := api.CreateDirectoryRequest{ID: uploadID, Mode: protocol.Standard, ExpectedHead: "0", Standard: &protocol.StandardMetadata{Name: "Large tree"}}
	if _, err := repo.CreateDirectory(t.Context(), ownerA, folder); err != nil {
		t.Fatal(err)
	}
	// A small cache forces the same disk spill without a huge test fixture.
	if _, err := db.Exec("PRAGMA temp.cache_size=1"); err != nil {
		t.Fatal(err)
	}
	const count = 2000
	if _, err := db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO directories(account_id,id,parent_id,name,revision) SELECT ?,printf('%032x',x),?,printf('Folder %d',x),1 FROM n`, count, ownerA, uploadID); err != nil {
		t.Fatal(err)
	}
	// A foreign tree with the same IDs must survive the complete deletion.
	if _, err := db.Exec(`INSERT INTO directories SELECT ?,id,parent_id,name,revision FROM directories WHERE account_id=?`, ownerB, ownerA); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteEntry(t.Context(), ownerA, uploadID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		account protocol.ID
		want    int
	}{{ownerA, 0}, {ownerB, count + 1}} {
		var remaining int
		if err := db.QueryRow(`SELECT count(*) FROM directories WHERE account_id=?`, tc.account).Scan(&remaining); err != nil || remaining != tc.want {
			t.Fatalf("remaining=%d want=%d: %v", remaining, tc.want, err)
		}
	}
}
