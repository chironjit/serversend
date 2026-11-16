package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chironjit/serversend/apps/server/internal/auth"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/pquerna/otp/totp"
)

// onboardedClient signs a new account in through the real client: temporary
// password, replacement, TOTP enrollment and a device session.
func onboardedClient(t *testing.T, origin, pin, username, temporary string) (*client.Client, string) {
	t.Helper()
	c, err := client.New(origin, pin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	ctx := t.Context()
	attempt, err := c.Password(ctx, username, temporary)
	if err != nil || attempt.NextStep != "replace_password" {
		t.Fatalf("temporary login: %q %v", attempt.NextStep, err)
	}
	password := "permanent password for " + username
	if err := c.ReplacePassword(ctx, attempt.Challenge, password); err != nil {
		t.Fatal(err)
	}
	if attempt, err = c.Password(ctx, username, password); err != nil || attempt.NextStep != "enroll_totp" {
		t.Fatalf("enrollment login: %q %v", attempt.NextStep, err)
	}
	enrollment, err := c.EnrollTOTP(ctx, attempt.Challenge)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.GenerateCode(enrollment.Secret, time.Now())
	codes, err := c.ConfirmTOTP(ctx, attempt.Challenge, code)
	if err != nil || len(codes) != 10 {
		t.Fatalf("confirmation: %v", err)
	}
	if attempt, err = c.Password(ctx, username, password); err != nil || attempt.NextStep != "second_factor" {
		t.Fatalf("login: %q %v", attempt.NextStep, err)
	}
	// The enrollment code's time step is spent; a recovery code signs in.
	session, err := c.SignIn(ctx, attempt.Challenge, codes[0], "test device")
	if err != nil || session.Username != username || session.AccessToken != "" {
		t.Fatalf("session: %+v %v", session, err)
	}
	return c, password
}

func writeFixture(t *testing.T, dir, name string, size int) (string, []byte) {
	t.Helper()
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, data
}

func TestClientUploadsAndRestoresFilesEndToEnd(t *testing.T) {
	config := fixture(t)
	s, err := New(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, listener) }()
	t.Cleanup(func() { cancel(); <-done })
	origin := "https://" + listener.Addr().String()

	probe, err := client.Probe(t.Context(), origin)
	if err != nil || len(probe.Fingerprint) != 64 || probe.PubliclyTrusted {
		t.Fatalf("probe: %+v %v", probe, err)
	}
	if strings.ReplaceAll(s.CertificateFingerprint(), " ", "") != probe.Fingerprint {
		t.Fatalf("operator fingerprint %q differs from the probed certificate", s.CertificateFingerprint())
	}
	wrongPin, err := client.New(origin, probe.Fingerprint[:63]+map[bool]string{true: "0", false: "1"}[probe.Fingerprint[63] != '0'])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrongPin.Password(t.Context(), "admin", "a secret that must not be sent"); !errors.Is(err, client.ErrIdentityChanged) {
		t.Fatalf("unapproved certificate received a request: %v", err)
	}

	issued, err := s.Accounts().BootstrapAdmin(t.Context(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := onboardedClient(t, origin, probe.Fingerprint, "admin", issued.TemporaryPassword)
	if page, err := admin.Catalog(t.Context()); err != nil || len(page.Entries) != 0 || page.CatalogRevision != "0" {
		t.Fatalf("initial catalog: %+v %v", page, err)
	}

	source := t.TempDir()
	fixtures := map[string][]byte{}
	for name, size := range map[string]int{"empty.bin": 0, "small.txt": 1234, "exactly-one-chunk.bin": 4 << 20, "multi chunk ünïcode.bin": 9<<20 + 17} {
		path, data := writeFixture(t, source, name, size)
		fixtures[name] = data
		var last, total int64 = -1, -1
		result, err := admin.Upload(t.Context(), path, func(done, all int64) {
			if done < last {
				t.Errorf("progress went backwards for %s", name)
			}
			last, total = done, all
		})
		if err != nil || result.State != protocol.UploadCommitted {
			t.Fatalf("upload %s: %+v %v", name, result, err)
		}
		if last != int64(size) || total != int64(size) {
			t.Fatalf("progress for %s ended at %d of %d", name, last, total)
		}
	}
	if _, err := admin.Upload(t.Context(), source, nil); err == nil {
		t.Fatal("directory uploaded as a file")
	}

	page, err := admin.Catalog(t.Context())
	if err != nil || len(page.Entries) != len(fixtures) || page.CatalogRevision != "4" {
		t.Fatalf("catalog: %d entries at %s: %v", len(page.Entries), page.CatalogRevision, err)
	}
	restored := t.TempDir()
	for _, entry := range page.Entries {
		destination := filepath.Join(restored, entry.Standard.Name)
		if err := admin.Download(t.Context(), entry, destination, nil); err != nil {
			t.Fatalf("download %s: %v", entry.Standard.Name, err)
		}
		data, err := os.ReadFile(destination)
		if err != nil || !bytes.Equal(data, fixtures[entry.Standard.Name]) {
			t.Fatalf("restored %s differs from its source: %v", entry.Standard.Name, err)
		}
		if _, err := os.Stat(destination + ".serversend-partial"); !os.IsNotExist(err) {
			t.Fatal("partial download left behind")
		}
	}

	// Stored objects are complete on the data volume, and none is in staging.
	var objects, bytesStored int64
	_ = filepath.Walk(filepath.Join(config.DataDir, "objects"), func(_ string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			objects++
			bytesStored += info.Size()
		}
		return nil
	})
	if objects != 5 || bytesStored != 1234+(4<<20)+(9<<20)+17 {
		t.Fatalf("stored %d objects, %d bytes", objects, bytesStored)
	}

	// A second account sees nothing and cannot read the first account's files.
	// Administrator actions have no session-backed caller yet, so the second
	// account is issued directly, exactly as the service would store it.
	temporary := "temporary password for second"
	hash, err := auth.NewArgon2id().Hash(temporary)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := s.db.Exec(`INSERT INTO accounts(id,username,role,state,password_hash,created_at,temporary_expires_at) VALUES('bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','second','user','password_required',?,?,?)`, hash, now.UnixMilli(), now.Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second) // Let the global sign-in attempt budget refill.
	other, _ := onboardedClient(t, origin, probe.Fingerprint, "second", temporary)
	if mine, err := other.Catalog(t.Context()); err != nil || len(mine.Entries) != 0 {
		t.Fatalf("foreign catalog visible: %v", err)
	}
	var failure api.Error
	if err := other.Download(t.Context(), page.Entries[0], filepath.Join(restored, "stolen"), nil); !errors.As(err, &failure) || failure.Code != api.NotFound {
		t.Fatalf("foreign file readable: %v", err)
	}

	// Sign-out ends the session on the server, not only locally.
	if err := other.SignOut(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Catalog(t.Context()); !errors.As(err, &failure) || failure.Code != api.AuthenticationRequired {
		t.Fatalf("signed-out catalog: %v", err)
	}
	// Disabling an account revokes its live session on the next request.
	if _, err := s.db.Exec(`UPDATE accounts SET disabled=1,auth_epoch=auth_epoch+1 WHERE username='admin'`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Catalog(t.Context()); !errors.As(err, &failure) || failure.Code != api.AuthenticationRequired {
		t.Fatalf("disabled account kept its session: %v", err)
	}
}
