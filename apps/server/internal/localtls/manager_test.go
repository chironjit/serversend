package localtls

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/testutil"
)

func privateConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
func addresses() []netip.Addr {
	return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("192.168.1.20")}
}
func clockForTest() *testutil.Clock {
	return testutil.NewClock(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC))
}
func mustOpen(t *testing.T, dir string, ips []netip.Addr, clock *testutil.Clock) *Manager {
	t.Helper()
	m, err := open(dir, ips, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func verify(t *testing.T, m *Manager, pair *tls.Certificate, ip string, now time.Time) error {
	t.Helper()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(m.PublicCA()) {
		t.Fatal("invalid CA export")
	}
	_, err := pair.Leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: ip, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	return err
}

func TestPrivateCertificatesRestartRenewalAndAddressChange(t *testing.T) {
	dir := privateConfig(t)
	clock := clockForTest()
	m := mustOpen(t, dir, addresses(), clock)
	pair, err := m.TLSConfig().GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), pair.Leaf.Raw...)
	root := m.PublicCA()
	for _, ip := range []string{"127.0.0.1", "192.168.1.20"} {
		if err := verify(t, m, pair, ip, clock.Now()); err != nil {
			t.Fatal(err)
		}
	}
	for _, ip := range []string{"192.168.1.21", "localhost"} {
		if err := verify(t, m, pair, ip, clock.Now()); err == nil {
			t.Fatalf("unexpected name accepted: %s", ip)
		}
	}
	if len(pair.Leaf.DNSNames) != 0 {
		t.Fatal("domain appeared in certificate")
	}
	if bytes.Contains(root, []byte("PRIVATE KEY")) {
		t.Fatal("CA export includes a private key")
	}
	m = mustOpen(t, dir, addresses(), clock)
	pair, err = m.certificate(nil)
	if err != nil || !bytes.Equal(pair.Leaf.Raw, original) || !bytes.Equal(root, m.PublicCA()) {
		t.Fatal("restart replaced TLS trust/material")
	}
	clock.Advance(61 * 24 * time.Hour)
	renewed, err := m.certificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(renewed.Leaf.Raw, original) || !bytes.Equal(root, m.PublicCA()) {
		t.Fatal("renewal failed or replaced authority")
	}
	if err := verify(t, m, renewed, "127.0.0.1", clock.Now()); err != nil {
		t.Fatal(err)
	}
	unchanged, err := m.certificate(nil)
	if err != nil || !bytes.Equal(unchanged.Leaf.Raw, renewed.Leaf.Raw) {
		t.Fatal("renewed repeatedly on handshakes")
	}
	updated := []netip.Addr{netip.MustParseAddr("10.10.10.50")}
	m = mustOpen(t, dir, updated, clock)
	pair, err = m.certificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(root, m.PublicCA()) {
		t.Fatal("IP change replaced trust root")
	}
	if err := verify(t, m, pair, "10.10.10.50", clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := verify(t, m, pair, "192.168.1.20", clock.Now()); err == nil {
		t.Fatal("removed IP remains valid")
	}
	other := mustOpen(t, privateConfig(t), updated, clock)
	if bytes.Equal(other.PublicCA(), m.PublicCA()) || verify(t, other, pair, "10.10.10.50", clock.Now()) == nil {
		t.Fatal("independent deployment trusted")
	}
}

func TestExpiredLeafRenewsButExpiredAuthorityDoesNot(t *testing.T) {
	clock := clockForTest()
	dir := privateConfig(t)
	m := mustOpen(t, dir, addresses(), clock)
	root := m.PublicCA()
	clock.Advance(100 * 24 * time.Hour)
	m = mustOpen(t, dir, addresses(), clock)
	pair, err := m.certificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(t, m, pair, "127.0.0.1", clock.Now()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(root, m.PublicCA()) {
		t.Fatal("expired leaf replaced CA")
	}
	clock.Advance(11 * 365 * 24 * time.Hour)
	if _, err := m.certificate(nil); err == nil {
		t.Fatal("expired root served")
	}
	if _, err := open(dir, addresses(), clock.Now); err == nil {
		t.Fatal("expired root silently replaced")
	}
	actual, err := os.ReadFile(filepath.Join(dir, Directory, CAFile))
	if err != nil || !bytes.Equal(root, actual) {
		t.Fatal("expired authority was modified")
	}
}

func TestPrivateMaterialFailuresDoNotRotateTrust(t *testing.T) {
	for _, scenario := range []string{"root corrupt", "leaf corrupt", "export corrupt", "root missing", "public root missing", "root symlink", "leaf symlink", "public root symlink", "public key permissions", "directory symlink", "future clock", "expired issuer"} {
		t.Run(scenario, func(t *testing.T) {
			dir := privateConfig(t)
			clock := clockForTest()
			m := mustOpen(t, dir, addresses(), clock)
			caPath := filepath.Join(dir, Directory, authorityFile)
			leafPath := filepath.Join(dir, Directory, serverFile)
			publicPath := filepath.Join(dir, Directory, CAFile)
			caBytes, err := os.ReadFile(caPath)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "root corrupt":
				if err := os.WriteFile(caPath, []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			case "leaf corrupt":
				if err := os.WriteFile(leafPath, []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			case "export corrupt":
				if err := os.WriteFile(publicPath, []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			case "root missing":
				if err := os.Remove(caPath); err != nil {
					t.Fatal(err)
				}
			case "public root missing":
				if err := os.Remove(publicPath); err != nil {
					t.Fatal(err)
				}
			case "root symlink", "leaf symlink", "public root symlink":
				path := caPath
				if scenario == "leaf symlink" {
					path = leafPath
				}
				if scenario == "public root symlink" {
					path = publicPath
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(t.TempDir(), "target"), path); err != nil {
					t.Fatal(err)
				}
			case "public key permissions":
				if err := os.Chmod(caPath, 0644); err != nil {
					t.Fatal(err)
				}
			case "directory symlink":
				real := filepath.Join(dir, "saved-tls")
				if err := os.Rename(filepath.Join(dir, Directory), real); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(real, filepath.Join(dir, Directory)); err != nil {
					t.Fatal(err)
				}
			case "future clock":
				clock.Advance(-time.Hour)
			case "expired issuer":
				clock.Advance(11 * 365 * 24 * time.Hour)
			}
			got, err := open(dir, addresses(), clock.Now)
			if scenario == "public root missing" {
				if err != nil || !bytes.Equal(got.PublicCA(), m.PublicCA()) {
					t.Fatalf("public export repair rotated CA: %v", err)
				}
			} else if err == nil || got != nil {
				t.Fatal("invalid TLS material accepted")
			}
			if scenario != "root corrupt" && scenario != "root missing" && scenario != "root symlink" {
				actual, err := os.ReadFile(caPath)
				if err != nil || !bytes.Equal(actual, caBytes) {
					t.Fatal("failure mutated authority")
				}
			}
		})
	}
}

func TestConcurrentRenewalAndBoundedAddressValidation(t *testing.T) {
	dir := privateConfig(t)
	clock := clockForTest()
	m := mustOpen(t, dir, addresses(), clock)
	clock.Advance(61 * 24 * time.Hour)
	var wg sync.WaitGroup
	results := make(chan *tls.Certificate, 16)
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Go(func() { pair, err := m.certificate(nil); results <- pair; failures <- err })
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first *tls.Certificate
	for pair := range results {
		if first == nil {
			first = pair
		}
		if first != pair {
			t.Fatal("racing handshakes published multiple leaves")
		}
	}
	for _, ips := range [][]netip.Addr{nil, {netip.MustParseAddr("0.0.0.0")}, {netip.MustParseAddr("224.0.0.1")}, {netip.MustParseAddr("255.255.255.255")}, {netip.MustParseAddr("::1")}, {addresses()[0], addresses()[0]}, make([]netip.Addr, 17)} {
		if _, err := open(dir, ips, clock.Now); err == nil {
			t.Fatalf("invalid addresses accepted: %v", ips)
		}
	}
	if _, err := open("relative", addresses(), clock.Now); err == nil {
		t.Fatal("relative configuration path accepted")
	}
}

func TestRenewalFailureAndRetry(t *testing.T) {
	dir := privateConfig(t)
	clock := clockForTest()
	m := mustOpen(t, dir, addresses(), clock)
	old := m.leaf.Leaf.Raw
	root := m.PublicCA()
	clock.Advance(61 * 24 * time.Hour)
	leafPath := filepath.Join(dir, Directory, serverFile)
	if err := os.Chmod(leafPath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.certificate(nil); err == nil {
		t.Fatal("unsafe key permissions ignored")
	}
	if !bytes.Equal(m.leaf.Leaf.Raw, old) {
		t.Fatal("failed renewal published new memory state")
	}
	if err := os.Chmod(leafPath, 0600); err != nil {
		t.Fatal(err)
	}
	pair, err := m.certificate(nil)
	if err != nil || bytes.Equal(pair.Leaf.Raw, old) || !bytes.Equal(root, m.PublicCA()) {
		t.Fatalf("retry failed: %v", err)
	}
	if err := os.Rename(filepath.Join(dir, Directory), filepath.Join(dir, "offline")); err != nil {
		t.Fatal(err)
	}
	clock.Advance(61 * 24 * time.Hour)
	if _, err := m.certificate(nil); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("filesystem failure: %v", err)
	}
}

func TestAuthorityExpiryCapsLeafWithoutRepeatedRenewal(t *testing.T) {
	dir := privateConfig(t)
	clock := clockForTest()
	m := mustOpen(t, dir, addresses(), clock)
	clock.Advance(m.ca.Leaf.NotAfter.Sub(clock.Now()) - 10*24*time.Hour)
	m = mustOpen(t, dir, addresses(), clock)
	first, err := m.certificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Leaf.NotAfter.After(m.ca.Leaf.NotAfter) {
		t.Fatal("leaf outlives authority")
	}
	second, err := m.certificate(nil)
	if err != nil || first != second {
		t.Fatal("shortened leaf renewed on every handshake")
	}
	if err := verify(t, m, first, "127.0.0.1", clock.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestForeignSignedLeafFailsWithoutReplacement(t *testing.T) {
	clock := clockForTest()
	dir := privateConfig(t)
	m := mustOpen(t, dir, addresses(), clock)
	otherDir := privateConfig(t)
	mustOpen(t, otherDir, addresses(), clock)
	foreign, err := os.ReadFile(filepath.Join(otherDir, Directory, serverFile))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, Directory, serverFile)
	if err := os.WriteFile(path, foreign, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := open(dir, addresses(), clock.Now); err == nil {
		t.Fatal("foreign-issued leaf accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, foreign) {
		t.Fatal("invalid leaf silently overwritten")
	}
	root, err := os.ReadFile(filepath.Join(dir, Directory, CAFile))
	if err != nil || !bytes.Equal(root, m.PublicCA()) {
		t.Fatal("invalid leaf rotated root")
	}
}
