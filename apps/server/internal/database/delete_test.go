package database

import (
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"testing"
)

func TestDeleteTreeRejectsActiveUploadWithoutPartialChanges(t *testing.T) {
	db, _ := openDatabase(t)
	mustAccount(t, db, ownerA)
	mustAccount(t, db, ownerB)
	repo := NewRepository(db)
	folder := api.CreateDirectoryRequest{ID: uploadID, Mode: protocol.Standard, ExpectedHead: "0", Standard: &protocol.StandardMetadata{Name: "Folder"}}
	if _, err := repo.CreateDirectory(t.Context(), ownerA, folder); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DeleteEntry(t.Context(), ownerB, uploadID); err == nil {
		t.Fatal("foreign deletion accepted")
	}
	if _, err := db.Exec(`INSERT INTO uploads(account_id,id,state,mode,idempotency_key,expected_bytes,created_at,parent_id) VALUES(?,?,'open','standard','pending',0,0,?)`, ownerA, ownerB, uploadID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DeleteEntry(t.Context(), ownerA, uploadID); err == nil {
		t.Fatal("active upload deleted")
	}
	if err := repo.CheckDirectory(t.Context(), ownerA, uploadID); err != nil {
		t.Fatal("failed delete removed folder")
	}
	if _, err := db.Exec(`UPDATE uploads SET state='cancelled' WHERE account_id=?`, ownerA); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DeleteEntry(t.Context(), ownerA, uploadID); err != nil {
		t.Fatal(err)
	}
	if err := repo.CheckDirectory(t.Context(), ownerA, uploadID); err == nil {
		t.Fatal("folder still present")
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
	if _, err := repo.DeleteEntry(t.Context(), ownerA, uploadID); err != nil {
		t.Fatalf("expired upload blocked deletion: %v", err)
	}
	var remaining int
	if err := db.QueryRow(`SELECT count(*) FROM uploads WHERE account_id=?`, ownerA).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("orphaned upload: %d %v", remaining, err)
	}
}
