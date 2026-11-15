package protocol

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const IdentityProofLifetime = 2 * time.Minute
const IdentityClockSkew = 30 * time.Second
const IdentityExporterLabel = "EXPORTER-ServerSend-Identity-v1"

// ValidateOrigin requires a canonical HTTPS origin without a trailing slash.
// It does not verify DNS, certificate trust or network reachability.
func ValidateOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil || len(origin) > 512 || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return errors.New("endpoint must be a canonical HTTPS origin")
	}
	host := u.Hostname()
	if host == "" || host != strings.ToLower(host) || strings.ContainsAny(host, "%\\\r\n") {
		return errors.New("invalid endpoint hostname")
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 || n == 443 || strconv.FormatUint(n, 10) != port {
			return errors.New("noncanonical endpoint port")
		}
	}
	canonical := host
	if strings.Contains(host, ":") {
		ip := net.ParseIP(host)
		if ip == nil || ip.String() != host {
			return errors.New("invalid IPv6 endpoint")
		}
		canonical = "[" + host + "]"
	} else {
		for _, c := range host {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
				return errors.New("endpoint hostname must use ASCII DNS or IP form")
			}
		}
		if strings.HasSuffix(host, ".") {
			return errors.New("endpoint hostname must not end in a dot")
		}
		if len(host) > 253 {
			return errors.New("endpoint hostname is too long")
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return errors.New("invalid endpoint DNS label")
			}
		}
		if strings.Contains(host, ".") && strings.Trim(host, "0123456789.") == "" {
			if ip := net.ParseIP(host); ip == nil || ip.String() != host {
				return errors.New("noncanonical IPv4 endpoint")
			}
		}
	}
	if port != "" {
		canonical += ":" + port
	}
	if origin != "https://"+canonical {
		return errors.New("noncanonical endpoint origin")
	}
	return nil
}

func validateBase64URL(value string, size int) error {
	if len(value) != base64.RawURLEncoding.EncodedLen(size) {
		return errors.New("invalid base64url value length")
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(b) != size || base64.RawURLEncoding.EncodeToString(b) != value {
		return errors.New("invalid canonical base64url value")
	}
	return nil
}

type IdentityChallenge struct {
	Nonce  string `json:"nonce"`
	Origin string `json:"origin"`
}

func (c IdentityChallenge) Validate() error {
	if err := validateBase64URL(c.Nonce, 32); err != nil {
		return err
	}
	return ValidateOrigin(c.Origin)
}

// TrustedIdentity is public material established over verified TLS or compared
// with an administrator's fingerprint through an independent trusted channel.
type TrustedIdentity struct {
	ServerID  ID     `json:"server_id"`
	PublicKey string `json:"public_key"`
}

func (i TrustedIdentity) Validate() error {
	if err := i.ServerID.Validate(); err != nil {
		return err
	}
	return validateBase64URL(i.PublicKey, ed25519.PublicKeySize)
}

// Fingerprint is SHA-256 of the raw Ed25519 public key, lowercase hexadecimal.
func (i TrustedIdentity) Fingerprint() (Digest, error) {
	if err := i.Validate(); err != nil {
		return "", err
	}
	b, _ := base64.RawURLEncoding.DecodeString(i.PublicKey)
	sum := sha256.Sum256(b)
	return Digest(hex.EncodeToString(sum[:])), nil
}

type IdentityProof struct {
	ProtocolVersion Version `json:"protocol_version"`
	TrustedIdentity
	IdentityChallenge
	CertificateSHA256 Digest `json:"certificate_sha256"`
	ChannelBinding    string `json:"channel_binding"`
	IssuedAt          int64  `json:"issued_at"`
	ExpiresAt         int64  `json:"expires_at"`
	Signature         string `json:"signature"`
}

// SigningBytes is canonical ASCII: fields below in order, separated by LF,
// with no final LF. Signature itself is excluded. All inputs are canonical and
// cannot contain LF. Unix seconds avoid JSON and time-format ambiguities.
func (p IdentityProof) SigningBytes() ([]byte, error) {
	if p.ProtocolVersion != Version1 {
		return nil, ErrUnsupportedVersion
	}
	if err := p.TrustedIdentity.Validate(); err != nil {
		return nil, err
	}
	if err := p.IdentityChallenge.Validate(); err != nil {
		return nil, err
	}
	if err := p.CertificateSHA256.Validate(); err != nil {
		return nil, err
	}
	if err := validateBase64URL(p.ChannelBinding, 32); err != nil {
		return nil, err
	}
	if p.IssuedAt <= 0 || p.ExpiresAt <= p.IssuedAt || p.ExpiresAt-p.IssuedAt > int64(IdentityProofLifetime/time.Second) {
		return nil, errors.New("invalid identity proof lifetime")
	}
	return []byte(strings.Join([]string{"ServerSend identity proof v1", string(p.ServerID), p.PublicKey, p.Nonce, p.Origin, string(p.CertificateSHA256), p.ChannelBinding, strconv.FormatInt(p.IssuedAt, 10), strconv.FormatInt(p.ExpiresAt, 10)}, "\n")), nil
}

func (p IdentityProof) Validate() error {
	if _, err := p.SigningBytes(); err != nil {
		return err
	}
	return validateBase64URL(p.Signature, ed25519.SignatureSize)
}

// Verify requires the actual TLS leaf DER and 32-byte exporter from this connection, a fresh
// outstanding challenge, and independently established/pinned trust. TLS chain
// and hostname checks remain mandatory in the caller. No credential may be
// sent before both TLS policy and this proof pass. Consume the nonce on success.
func (p IdentityProof) Verify(challenge IdentityChallenge, trusted TrustedIdentity, leafDER, tlsExporter []byte, now time.Time) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if err := trusted.Validate(); err != nil {
		return err
	}
	if err := challenge.Validate(); err != nil {
		return err
	}
	if p.TrustedIdentity != trusted || p.IdentityChallenge != challenge || len(leafDER) == 0 {
		return errors.New("identity proof does not match connection or trust")
	}
	sum := sha256.Sum256(leafDER)
	if p.CertificateSHA256 != Digest(hex.EncodeToString(sum[:])) {
		return errors.New("identity proof certificate mismatch")
	}
	if len(tlsExporter) != 32 || p.ChannelBinding != base64.RawURLEncoding.EncodeToString(tlsExporter) {
		return errors.New("identity proof TLS channel mismatch")
	}
	if now.Unix() < p.IssuedAt-int64(IdentityClockSkew/time.Second) || now.Unix() >= p.ExpiresAt {
		return errors.New("identity proof is expired or from the future")
	}
	key, _ := base64.RawURLEncoding.DecodeString(p.PublicKey)
	signature, _ := base64.RawURLEncoding.DecodeString(p.Signature)
	message, _ := p.SigningBytes()
	if !ed25519.Verify(key, message, signature) {
		return errors.New("invalid identity signature")
	}
	return nil
}
