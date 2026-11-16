package localtls

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivateCertificatesSupportBrowserSignatureAlgorithms(t *testing.T) {
	m := mustOpen(t, privateConfig(t), addresses(), clockForTest())
	pair, err := m.certificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	// Firefox does not advertise Ed25519 certificate authentication. Offering
	// only P-256 must still succeed, without lowering the TLS version.
	hello := &tls.ClientHelloInfo{SupportedVersions: []uint16{tls.VersionTLS13}, SignatureSchemes: []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256}, CipherSuites: []uint16{tls.TLS_AES_128_GCM_SHA256}, SupportedCurves: []tls.CurveID{tls.CurveP256}, SupportedPoints: []uint8{0}}
	if err := hello.SupportsCertificate(pair); err != nil {
		t.Fatalf("browser cannot authenticate the server: %v", err)
	}
	for _, certificate := range []*x509.Certificate{m.ca.Leaf, pair.Leaf} {
		if certificate.PublicKeyAlgorithm != x509.ECDSA || certificate.SignatureAlgorithm != x509.ECDSAWithSHA256 {
			t.Fatalf("browser-incompatible certificate: %v / %v", certificate.PublicKeyAlgorithm, certificate.SignatureAlgorithm)
		}
	}
	if err := verify(t, m, pair, "127.0.0.1", m.now()); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyEd25519AuthorityPreservedUntilExplicitReplacement(t *testing.T) {
	dir := privateConfig(t)
	clock := clockForTest()
	tlsDir := filepath.Join(dir, Directory)
	if err := os.Mkdir(tlsDir, 0700); err != nil {
		t.Fatal(err)
	}
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Legacy ServerSend CA"}, NotBefore: clock.Now().Add(-time.Hour), NotAfter: clock.Now().AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	authority, err := encodePair(template, template, public, key, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := parsePair(authority)
	if err != nil {
		t.Fatal(err)
	}
	root := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Leaf.Raw})
	leafTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: clock.Now().Add(-time.Minute), NotAfter: clock.Now().Add(leafLifetime), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	for _, ip := range addresses() {
		leafTemplate.IPAddresses = append(leafTemplate.IPAddresses, ip.AsSlice())
	}
	leaf, err := encodePair(leafTemplate, ca.Leaf, public, key, key)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{authorityFile: authority, CAFile: root, serverFile: leaf} {
		if err := os.WriteFile(filepath.Join(tlsDir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := mustOpen(t, dir, addresses(), clock)
	if !bytes.Equal(root, m.PublicCA()) {
		t.Fatal("legacy authority silently replaced")
	}
	actual, err := os.ReadFile(filepath.Join(tlsDir, serverFile))
	if err != nil || !bytes.Equal(actual, leaf) {
		t.Fatal("legacy leaf silently replaced")
	}
	clock.Advance(61 * 24 * time.Hour)
	if _, err := m.certificate(nil); err != nil {
		t.Fatalf("legacy authority cannot renew: %v", err)
	}
	if !bytes.Equal(root, m.PublicCA()) {
		t.Fatal("renewal rotated legacy root")
	}
	// Explicit, reversible operator action: archive the complete TLS directory.
	backup := filepath.Join(dir, "tls-ed25519-backup")
	if err := os.Rename(tlsDir, backup); err != nil {
		t.Fatal(err)
	}
	upgraded := mustOpen(t, dir, addresses(), clock)
	if bytes.Equal(root, upgraded.PublicCA()) || upgraded.ca.Leaf.PublicKeyAlgorithm != x509.ECDSA {
		t.Fatal("explicit replacement did not generate ECDSA")
	}
	saved, err := os.ReadFile(filepath.Join(backup, authorityFile))
	if err != nil || !bytes.Equal(saved, authority) {
		t.Fatal("legacy backup changed")
	}
}
