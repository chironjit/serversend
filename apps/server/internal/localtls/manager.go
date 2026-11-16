package localtls

import (
	"bytes"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const Directory = "tls"
const CAFile = "ca.crt"
const authorityFile = "authority.pem"
const serverFile = "server.pem"

// Manager retains a stable private CA and renews IP certificates on handshakes.
// Like the rest of a deployment, it is owned by one server process at a time.
type Manager struct {
	mu   sync.Mutex
	dir  string
	ca   tls.Certificate
	ips  []netip.Addr
	leaf *tls.Certificate
	now  func() time.Time
}

// Open requires an existing private absolute config directory. ips contains the
// client-visible IPv4 addresses, which cannot be inferred from Docker's bind
// address. Generated keys are local and remain in config/tls across restarts.
func Open(configDir string, ips []netip.Addr) (*Manager, error) {
	return open(configDir, ips, time.Now)
}

func open(configDir string, ips []netip.Addr, now func() time.Time) (*Manager, error) {
	if !filepath.IsAbs(configDir) {
		return nil, errors.New("local TLS config path must be absolute")
	}
	if err := checkDirectory(configDir); err != nil {
		return nil, err
	}
	if len(ips) < 1 || len(ips) > 16 {
		return nil, errors.New("local TLS requires 1 to 16 IPv4 addresses")
	}
	seen := make(map[netip.Addr]bool)
	for _, ip := range ips {
		if !ip.Is4() || (!ip.IsGlobalUnicast() && !ip.IsLoopback()) || seen[ip] {
			return nil, errors.New("local TLS addresses must be distinct unicast IPv4 addresses")
		}
		seen[ip] = true
	}
	dir := filepath.Join(configDir, Directory)
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err := checkDirectory(dir); err != nil {
		return nil, err
	}
	m := &Manager{dir: dir, ips: append([]netip.Addr(nil), ips...), now: now}
	if err := m.loadAuthority(); err != nil {
		return nil, fmt.Errorf("private TLS authority: %w; restore the TLS configuration backup rather than deleting trust material", err)
	}
	data, err := readFile(filepath.Join(dir, serverFile), true)
	if err == nil {
		pair, parseErr := parsePair(data)
		if parseErr != nil {
			return nil, errors.New("private TLS certificate/key is corrupt; restore the TLS configuration backup")
		}
		// Existing keys/certificates must be intact and issued by this deployment,
		// even when expired or covering the previous configured IP address.
		if err := pair.Leaf.CheckSignatureFrom(m.ca.Leaf); err != nil || pair.Leaf.IsCA {
			return nil, errors.New("private TLS certificate has an unexpected issuer or purpose; restore the TLS configuration backup")
		}
		m.leaf = &pair
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if _, err := m.certificate(nil); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) loadAuthority() error {
	path := filepath.Join(m.dir, authorityFile)
	data, err := readFile(path, true)
	if errors.Is(err, os.ErrNotExist) {
		// Never replace a lost root while other TLS material still refers to it.
		for _, name := range []string{serverFile, CAFile} {
			if _, err := os.Lstat(filepath.Join(m.dir, name)); !errors.Is(err, os.ErrNotExist) {
				return errors.New("authority key is missing but TLS material exists")
			}
		}
		var generated []byte
		generated, err = createAuthority(m.now())
		if err != nil {
			return err
		}
		if err := writeFile(path, generated, true); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		data, err = readFile(path, true)
	}
	if err != nil {
		return err
	}
	pair, err := parsePair(data)
	if err != nil {
		return errors.New("authority certificate/key is corrupt")
	}
	ca := pair.Leaf
	if _, ok := pair.PrivateKey.(ed25519.PrivateKey); !ok {
		return errors.New("unexpected authority key type")
	}
	if !ca.IsCA || !ca.BasicConstraintsValid || ca.CheckSignatureFrom(ca) != nil {
		return errors.New("invalid private authority certificate")
	}
	if m.now().Before(ca.NotBefore) || !m.now().Before(ca.NotAfter) {
		return errors.New("private authority is expired or not yet valid; explicit trust repair is required")
	}
	m.ca = pair
	public := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})
	publicPath := filepath.Join(m.dir, CAFile)
	existing, err := readFile(publicPath, false)
	if errors.Is(err, os.ErrNotExist) {
		if err := writeFile(publicPath, public, true); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		existing, err = readFile(publicPath, false)
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(existing, public) {
		return errors.New("public authority certificate does not match private trust material")
	}
	return nil
}

func (m *Manager) certificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if now.Before(m.ca.Leaf.NotBefore) || !now.Before(m.ca.Leaf.NotAfter) {
		return nil, errors.New("private authority is expired or not yet valid; repair trust before connecting")
	}
	if m.leaf != nil && now.Before(m.leaf.Leaf.NotBefore) {
		return nil, errors.New("private TLS certificate is not yet valid; check the server clock")
	}
	if m.leaf != nil && now.Before(renewAt(m.leaf.Leaf)) && sameIPs(m.leaf.Leaf, m.ips) {
		return m.leaf, nil
	}
	data, err := createLeaf(m.ca, m.ips, now)
	if err != nil {
		return nil, err
	}
	pair, err := parsePair(data)
	if err != nil {
		return nil, err
	}
	// Refuse unexpected filesystem changes before replacing the leaf bundle.
	if _, err := readFile(filepath.Join(m.dir, serverFile), true); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := writeFile(filepath.Join(m.dir, serverFile), data, false); err != nil {
		return nil, fmt.Errorf("persist renewed private TLS certificate: %w", err)
	}
	m.leaf = &pair
	return m.leaf, nil
}

func sameIPs(cert *x509.Certificate, ips []netip.Addr) bool {
	if len(cert.IPAddresses) != len(ips) || len(cert.DNSNames) != 0 {
		return false
	}
	for _, ip := range ips {
		found := false
		for _, actual := range cert.IPAddresses {
			found = found || actual.Equal(ip.AsSlice())
		}
		if !found {
			return false
		}
	}
	return true
}

// TLSConfig renews the leaf before expiry without changing the authority or
// terminating existing connections. No certificate verification is bypassed.
func (m *Manager) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: m.certificate}
}

// PublicCA returns a defensive copy containing no private key. Export/trust it
// only through an authenticated operator channel; downloading it is not proof.
func (m *Manager) PublicCA() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: m.ca.Leaf.Raw})
}

// Scale renewal for leaves whose lifetime is capped by authority expiry, so
// freshly renewed short-lived certificates are not rewritten on every handshake.
func renewAt(cert *x509.Certificate) time.Time {
	window := min(renewBefore, cert.NotAfter.Sub(cert.NotBefore)/3)
	return cert.NotAfter.Add(-window)
}
