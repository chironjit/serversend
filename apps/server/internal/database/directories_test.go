package database

import (
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"testing"
)

func TestDirectoriesSurviveReopenAndRemainScoped(t *testing.T) {
	db, dir := openDatabase(t)
	mustAccount(t, db, ownerA)
	mustAccount(t, db, ownerB)
	request := api.CreateDirectoryRequest{ID: uploadID, Mode: protocol.Standard, ExpectedHead: "0", Standard: &protocol.StandardMetadata{Name: "Backups"}}
	entry, err := NewRepository(db).CreateDirectory(t.Context(), ownerA, request)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	reopened, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	repo := NewRepository(reopened)
	_, entries, err := repo.Catalog(t.Context(), ownerA, "", 1000)
	if err != nil || len(entries) != 1 || entries[0].ID != entry.ID || !entries[0].Directory {
		t.Fatalf("restored folders: %+v %v", entries, err)
	}
	if err := repo.CheckDirectory(t.Context(), ownerB, entry.ID); err == nil {
		t.Fatal("foreign parent visible")
	}
	_, entries, err = repo.Catalog(t.Context(), ownerB, "", 1000)
	if err != nil || len(entries) != 0 {
		t.Fatalf("foreign catalog: %+v %v", entries, err)
	}
}

func TestFileAndDirectoryIDsCannotCollide(t *testing.T) {
	db, _ := openDatabase(t)
	mustAccount(t, db, ownerA)
	repo := NewRepository(db)
	request := api.CreateDirectoryRequest{ID: uploadID, Mode: protocol.Standard, ExpectedHead: "0", Standard: &protocol.StandardMetadata{Name: "Folder"}}
	if _, err := repo.CreateDirectory(t.Context(), ownerA, request); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.CreateTransfer(t.Context(), Transfer{AccountID: ownerA, ID: ownerB, FileID: uploadID, IdempotencyKey: "collision"}); err == nil {
		t.Fatal("file accepted with directory ID")
	}
}
