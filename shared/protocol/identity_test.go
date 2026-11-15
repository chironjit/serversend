package protocol

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func signedProof(t *testing.T) (IdentityProof, ed25519.PrivateKey, []byte) {
	t.Helper()
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	leaf := []byte("test TLS leaf DER")
	sum := sha256.Sum256(leaf)
	p := IdentityProof{ProtocolVersion: 1, TrustedIdentity: TrustedIdentity{testID, base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey))}, IdentityChallenge: IdentityChallenge{base64.RawURLEncoding.EncodeToString(make([]byte, 32)), "https://server.example:8443"}, CertificateSHA256: Digest(hex.EncodeToString(sum[:])), ChannelBinding: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), IssuedAt: 1800000000, ExpiresAt: 1800000120}
	message, err := p.SigningBytes()
	if err != nil {
		t.Fatal(err)
	}
	p.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, message))
	return p, key, leaf
}

func TestIdentityProofTranscriptAndFingerprint(t *testing.T) {
	p, _, leaf := signedProof(t)
	const expected = "ServerSend identity proof v1\n11111111111111111111111111111111\nO2onvM62pC1io6jQKm8Nc2UyFXcd4kOmOsBIoYtZ2ik\nAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\nhttps://server.example:8443\n"
	message, err := p.SigningBytes()
	if err != nil || string(message) != expected+string(p.CertificateSHA256)+"\n"+p.ChannelBinding+"\n1800000000\n1800000120" {
		t.Fatalf("identity transcript changed: %q, %v", message, err)
	}
	if err := p.Verify(p.IdentityChallenge, p.TrustedIdentity, leaf, make([]byte, 32), time.Unix(p.IssuedAt, 0)); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := p.TrustedIdentity.Fingerprint()
	if err != nil || fingerprint.Validate() != nil {
		t.Fatalf("fingerprint: %q, %v", fingerprint, err)
	}
	// Certificate renewal requires a fresh valid proof for the new connection.
	newLeaf := []byte("renewed certificate DER")
	sum := sha256.Sum256(newLeaf)
	p.CertificateSHA256 = Digest(hex.EncodeToString(sum[:]))
	_, key, _ := signedProof(t)
	message, _ = p.SigningBytes()
	p.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, message))
	if err := p.Verify(p.IdentityChallenge, p.TrustedIdentity, newLeaf, make([]byte, 32), time.Unix(p.IssuedAt, 0)); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityRejectsReplayReplacementAndWrongConnection(t *testing.T) {
	for name, change := range map[string]func(*IdentityProof){
		"server replacement": func(p *IdentityProof) { p.ServerID = "22222222222222222222222222222222" },
		"key replacement":    func(p *IdentityProof) { p.PublicKey = base64.RawURLEncoding.EncodeToString(make([]byte, 32)) },
		"wrong endpoint":     func(p *IdentityProof) { p.Origin = "https://other.example" },
		"wrong nonce": func(p *IdentityProof) {
			p.Nonce = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("n", 32)))
		},
		"wrong certificate": func(p *IdentityProof) { p.CertificateSHA256 = Digest(strings.Repeat("0", 64)) },
		"wrong channel": func(p *IdentityProof) {
			p.ChannelBinding = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("n", 32)))
		},
		"tampered issued":      func(p *IdentityProof) { p.IssuedAt++ },
		"tampered expiry":      func(p *IdentityProof) { p.ExpiresAt-- },
		"bad signature":        func(p *IdentityProof) { p.Signature = base64.RawURLEncoding.EncodeToString(make([]byte, 64)) },
		"unsupported protocol": func(p *IdentityProof) { p.ProtocolVersion = 2 },
		"unbounded lifetime":   func(p *IdentityProof) { p.ExpiresAt++ },
		"zero lifetime":        func(p *IdentityProof) { p.ExpiresAt = p.IssuedAt },
		"bad nonce":            func(p *IdentityProof) { p.Nonce = "short" },
	} {
		t.Run(name, func(t *testing.T) {
			p, _, leaf := signedProof(t)
			challenge, trusted := p.IdentityChallenge, p.TrustedIdentity
			change(&p)
			if p.Verify(challenge, trusted, leaf, make([]byte, 32), time.Unix(1800000000, 0)) == nil {
				t.Fatal("invalid proof accepted")
			}
		})
	}
	p, _, leaf := signedProof(t)
	for _, now := range []int64{p.IssuedAt - 31, p.ExpiresAt, p.ExpiresAt + 1} {
		if p.Verify(p.IdentityChallenge, p.TrustedIdentity, leaf, make([]byte, 32), time.Unix(now, 0)) == nil {
			t.Fatal("stale/future proof accepted")
		}
	}
	if err := p.Verify(p.IdentityChallenge, p.TrustedIdentity, leaf, make([]byte, 32), time.Unix(p.IssuedAt-30, 0)); err != nil {
		t.Fatal(err)
	}
	if p.Verify(p.IdentityChallenge, p.TrustedIdentity, nil, make([]byte, 32), time.Unix(p.IssuedAt, 0)) == nil {
		t.Fatal("missing TLS binding accepted")
	}
	if p.Verify(p.IdentityChallenge, p.TrustedIdentity, leaf, nil, time.Unix(p.IssuedAt, 0)) == nil {
		t.Fatal("missing TLS exporter accepted")
	}
}

func TestEndpointOriginRules(t *testing.T) {
	for _, origin := range []string{"https://-bad.example", "https://bad-.example", "https://a..example", "https://127.00.0.1", "https://" + strings.Repeat("a", 64) + ".example"} {
		if ValidateOrigin(origin) == nil {
			t.Fatalf("invalid DNS/IP origin accepted: %q", origin)
		}
	}
	for _, origin := range []string{"https://server.example", "https://server.example:8443", "https://127.0.0.1:8443", "https://[::1]:8443", "https://host.tailnet.ts.net"} {
		if err := ValidateOrigin(origin); err != nil {
			t.Fatalf("%s: %v", origin, err)
		}
	}
	for _, origin := range []string{"", "http://server.example", "https://user:pass@server.example", "https://server.example/", "https://server.example/path", "https://server.example?x=1", "https://server.example?", "https://server.example#frag", "https://server.example:", "https://server.example:443", "https://server.example:08443", "https://server.example:0", "https://server.example:65536", "https://SERVER.example", "https://server.example.", "https://[0:0:0:0:0:0:0:1]", "https://server\n.example", "https://sérver.example", "https://" + strings.Repeat("a", 513)} {
		if ValidateOrigin(origin) == nil {
			t.Fatalf("unsafe/noncanonical endpoint accepted: %q", origin)
		}
	}
}
