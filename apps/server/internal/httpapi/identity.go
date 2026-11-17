package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
)

type certificateContextKey struct{}
type connectionCertificate struct {
	mu   sync.Mutex
	leaf []byte
}

// IdentityConnectionContext must run before TLS, for both TCP and QUIC.
func IdentityConnectionContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, certificateContextKey{}, &connectionCertificate{})
}

// BindIdentityCertificate records the actual certificate selected by this TLS
// handshake. No request-provided certificate or exporter is ever signed.
func BindIdentityCertificate(config *tls.Config) *tls.Config {
	out := config.Clone()
	out.SessionTicketsDisabled = true
	out.Certificates = nil
	out.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		var certificate *tls.Certificate
		var err error
		if config.GetCertificate != nil {
			certificate, err = config.GetCertificate(hello)
		} else if len(config.Certificates) > 0 {
			certificate = &config.Certificates[0]
		}
		if err == nil && certificate != nil && len(certificate.Certificate) > 0 {
			if hello != nil && hello.Context() != nil {
				if record, ok := hello.Context().Value(certificateContextKey{}).(*connectionCertificate); ok {
					record.mu.Lock()
					record.leaf = append([]byte(nil), certificate.Certificate[0]...)
					record.mu.Unlock()
				}
			}
		}
		return certificate, err
	}
	return out
}

func WithIdentity(sign func(context.Context, *protocol.IdentityProof) error) Option {
	return func(o *options) {
		var mu sync.Mutex
		var start time.Time
		count := 0
		o.signIdentity = func(ctx context.Context, p *protocol.IdentityProof) error {
			mu.Lock()
			now := time.Now()
			if now.Sub(start) >= time.Second {
				start = now
				count = 0
			}
			count++
			allowed := count <= 64
			mu.Unlock()
			if !allowed {
				return api.Error{Code: api.Overloaded, Message: "Identity verification is busy; try again shortly", RetryAfterSeconds: 1}
			}
			return sign(ctx, p)
		}
	}
}
func serveIdentity(w http.ResponseWriter, r *http.Request, sign func(context.Context, *protocol.IdentityProof) error) bool {
	if r.URL.Path != api.BasePath+"/identity/proof" {
		return false
	}
	if r.Method != "POST" {
		writeError(w, r, 405, api.InvalidRequest, "Use POST")
		return true
	}
	if !sameOrigin(w, r) {
		return true
	}
	var challenge protocol.IdentityChallenge
	if err := decodeSessionBody(w, r, &challenge); err != nil || challenge.Validate() != nil || challenge.Origin != "https://"+r.Host || r.TLS == nil || r.TLS.Version != tls.VersionTLS13 {
		writeError(w, r, 400, api.InvalidRequest, "Use this server's direct HTTPS origin and a fresh nonce")
		return true
	}
	record, ok := r.Context().Value(certificateContextKey{}).(*connectionCertificate)
	if !ok || sign == nil {
		writeError(w, r, 503, api.Unavailable, "Identity proof unavailable")
		return true
	}
	record.mu.Lock()
	leaf := append([]byte(nil), record.leaf...)
	record.mu.Unlock()
	exporter, err := r.TLS.ExportKeyingMaterial(protocol.IdentityExporterLabel, nil, 32)
	if err != nil || len(leaf) == 0 {
		writeError(w, r, 503, api.Unavailable, "Identity proof unavailable")
		return true
	}
	digest := sha256.Sum256(leaf)
	now := time.Now()
	proof := protocol.IdentityProof{ProtocolVersion: protocol.Version1, IdentityChallenge: challenge, CertificateSHA256: protocol.Digest(hex.EncodeToString(digest[:])), ChannelBinding: base64.RawURLEncoding.EncodeToString(exporter), IssuedAt: now.Unix(), ExpiresAt: now.Add(protocol.IdentityProofLifetime).Unix()}
	if err = sign(r.Context(), &proof); err != nil {
		writeFailure(w, r, err)
	} else {
		writeJSON(w, r, 200, proof)
	}
	return true
}
