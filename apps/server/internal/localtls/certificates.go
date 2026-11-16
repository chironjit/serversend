// Package localtls provisions private, per-deployment HTTPS certificates for
// numeric addresses. It never contacts DNS, an ACME service or a public CA.
package localtls

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/netip"
	"time"
)

const leafLifetime = 90 * 24 * time.Hour
const renewBefore = 30 * 24 * time.Hour

func serialNumber() (*big.Int, error) {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	return n.Add(n, big.NewInt(1)), nil
}

func encodePair(template, parent *x509.Certificate, public crypto.PublicKey, key, signer crypto.Signer) ([]byte, error) {
	der, err := x509.CreateCertificate(rand.Reader, template, parent, public, signer)
	if err != nil {
		return nil, err
	}
	encodedKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return append(data, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey})...), nil
}

func createAuthority(now time.Time) ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "ServerSend private certificate authority"},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(10, 0, 0),
		IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	return encodePair(template, template, key.Public(), key, key)
}

func parsePair(data []byte) (tls.Certificate, error) {
	pair, err := tls.X509KeyPair(data, data)
	if err != nil {
		return tls.Certificate{}, err
	}
	if len(pair.Certificate) != 1 {
		return tls.Certificate{}, errors.New("expected a single local certificate")
	}
	pair.Leaf, err = x509.ParseCertificate(pair.Certificate[0])
	return pair, err
}

func createLeaf(ca tls.Certificate, ips []netip.Addr, now time.Time) ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}
	expires := now.Add(leafLifetime)
	if ca.Leaf.NotAfter.Before(expires) {
		expires = ca.Leaf.NotAfter
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "ServerSend private HTTPS"},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: expires,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, ip := range ips {
		template.IPAddresses = append(template.IPAddresses, net.IP(ip.AsSlice()))
	}
	return encodePair(template, ca.Leaf, key.Public(), key, ca.PrivateKey.(crypto.Signer))
}
