package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/apps/server/internal/database"
)

func TestCommandArguments(t *testing.T) {
	if err := run(t.Context(), []string{"-help"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"unexpected"}, {"-unknown"}, {"-check", "-bootstrap-admin", "admin"}, {"-bootstrap-admin", "admin", "-recover-bootstrap-admin", "admin"}, {"-restore", "/backup"}, {"-restore-as", "clone"}, {"-backup", "/a", "-restore", "/b", "-restore-as", "replace"}} {
		if err := run(t.Context(), args, io.Discard); err == nil {
			t.Fatalf("accepted invalid command arguments: %v", args)
		}
	}
}

func TestIssueAdministratorPrintsCredentialOnceAndExplainsFailures(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	accounts, err := auth.New(database.NewRepository(db), auth.NewArgon2id(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := issueAdministrator(t.Context(), accounts, "Admin", "", &out); err != nil {
		t.Fatal(err)
	}
	first := out.String()
	if !strings.Contains(first, "Administrator: admin\n") || !strings.Contains(first, "Temporary password: ") {
		t.Fatalf("operator output: %q", first)
	}
	out.Reset()
	if err := issueAdministrator(t.Context(), accounts, "other", "", &out); err == nil || !strings.Contains(err.Error(), "-recover-bootstrap-admin") || out.Len() != 0 {
		t.Fatalf("second bootstrap: %v", err)
	}
	if err := issueAdministrator(t.Context(), accounts, "bad name", "", &out); err == nil || out.Len() != 0 {
		t.Fatal("invalid username accepted")
	}
	if err := issueAdministrator(t.Context(), accounts, "", "other", &out); err == nil || out.Len() != 0 {
		t.Fatal("recovery issued for an unknown administrator")
	}
	if err := issueAdministrator(t.Context(), accounts, "", "admin", &out); err != nil || out.String() == first || !strings.Contains(out.String(), "Temporary password: ") {
		t.Fatalf("bootstrap recovery: %v", err)
	}
}
