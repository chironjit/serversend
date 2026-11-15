package tests

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

// Independently compose a proof producer and consumer over verified TLS.
// Relaying a signed proof across connections must fail even when the origin,
// certificate, identity key and challenge are all identical.
func TestIdentityProofCannotMoveBetweenTLSConnections(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	trusted := protocol.TrustedIdentity{ServerID: "11111111111111111111111111111111", PublicKey: base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey))}
	server := httptest.NewUnstartedServer(nil)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		challenge, err := api.DecodeIdentityChallenge(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		exporter, err := r.TLS.ExportKeyingMaterial(protocol.IdentityExporterLabel, nil, 32)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		sum := sha256.Sum256(server.Certificate().Raw)
		proof := protocol.IdentityProof{ProtocolVersion: 1, TrustedIdentity: trusted, IdentityChallenge: challenge, CertificateSHA256: protocol.Digest(hex.EncodeToString(sum[:])), ChannelBinding: base64.RawURLEncoding.EncodeToString(exporter), IssuedAt: 1800000000, ExpiresAt: 1800000120}
		message, err := proof.SigningBytes()
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		proof.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, message))
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(proof); err != nil {
			t.Error(err)
		}
	})
	server.StartTLS()
	defer server.Close()
	client := server.Client() // Test CA roots, with normal hostname/chain verification.
	defer client.CloseIdleConnections()
	challenge := protocol.IdentityChallenge{Nonce: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), Origin: server.URL}
	requestBody, err := json.Marshal(challenge)
	if err != nil {
		t.Fatal(err)
	}
	fetch := func() (protocol.IdentityProof, []byte, []byte) {
		t.Helper()
		response, err := client.Post(server.URL+api.BasePath+"/identity/proof", "application/json", bytes.NewReader(requestBody))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 || response.TLS == nil || response.TLS.Version != tls.VersionTLS13 {
			t.Fatalf("invalid TLS response: %v", response.Status)
		}
		proof, err := api.DecodeIdentityProof(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		exporter, err := response.TLS.ExportKeyingMaterial(protocol.IdentityExporterLabel, nil, 32)
		if err != nil {
			t.Fatal(err)
		}
		return proof, response.TLS.PeerCertificates[0].Raw, exporter
	}
	first, leaf, firstExporter := fetch()
	if err := first.Verify(challenge, trusted, leaf, firstExporter, time.Unix(1800000000, 0)); err != nil {
		t.Fatal(err)
	}
	client.CloseIdleConnections()
	second, secondLeaf, secondExporter := fetch()
	if bytes.Equal(firstExporter, secondExporter) {
		t.Fatal("test did not create a distinct TLS channel")
	}
	if err := second.Verify(challenge, trusted, secondLeaf, secondExporter, time.Unix(1800000000, 0)); err != nil {
		t.Fatal(err)
	}
	if first.Verify(challenge, trusted, secondLeaf, secondExporter, time.Unix(1800000000, 0)) == nil {
		t.Fatal("a proof was relayed across TLS connections")
	}
}
