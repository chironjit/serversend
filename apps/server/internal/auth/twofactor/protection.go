package twofactor

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base32"
	"io"

	"github.com/chironjit/serversend/shared/protocol"
)

const (
	secretEnvelopeVersion byte = 1
	protectedSecretBytes       = 1 + 12 + 32 + 16 // version, GCM nonce, Base32 secret, GCM tag
)

// SecretProtector encrypts canonical enrollment secrets under an independently
// managed server configuration key. It does not load, create, persist or rotate
// that key. Keep the key with deployment backups and never derive it from the
// public server identity or a password. The AES-256-GCM nonce is random for each
// seal; rotate the key well before 2^32 seals, including across process restarts.
// Instances are safe for concurrent use.
type SecretProtector struct {
	aead cipher.AEAD
}

func NewSecretProtector(key []byte) (*SecretProtector, error) {
	if len(key) != 32 {
		return nil, ErrProtectionKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrProtectionKey
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrProtectedSecret
	}
	return &SecretProtector{aead: aead}, nil
}

// Seal returns a versioned, fixed-size binary envelope. The scope is authenticated
// but not copied into the envelope; supply the same canonical server/account IDs
// to Open. Only the server's canonical 20-byte Base32 TOTP secrets are accepted.
func (p *SecretProtector) Seal(scope protocol.Scope, secret string) ([]byte, error) {
	return p.seal(scope, secret, rand.Reader)
}

func (p *SecretProtector) seal(scope protocol.Scope, secret string, random io.Reader) ([]byte, error) {
	if p == nil || p.aead == nil {
		return nil, ErrProtectedSecret
	}
	if err := scope.Validate(); err != nil {
		return nil, ErrInvalidScope
	}
	if !canonicalSecret(secret) {
		return nil, ErrInvalidSecret
	}
	envelope := make([]byte, 1+p.aead.NonceSize(), protectedSecretBytes)
	envelope[0] = secretEnvelopeVersion
	nonce := envelope[1:]
	if _, err := io.ReadFull(random, nonce); err != nil {
		return nil, ErrProtectedSecret
	}
	return p.aead.Seal(envelope, nonce, []byte(secret), secretAdditionalData(scope)), nil
}

// Open returns the secret only after authenticating the envelope and its scope.
// Unknown versions, malformed lengths, wrong keys and changed contexts all fail
// without returning partial plaintext. The envelope alone does not prevent
// rollback to an older valid secret for the same account; durable enrollment
// transitions and replay-watermark consumption belong to the account service.
func (p *SecretProtector) Open(scope protocol.Scope, envelope []byte) (string, error) {
	if p == nil || p.aead == nil {
		return "", ErrProtectedSecret
	}
	if err := scope.Validate(); err != nil {
		return "", ErrInvalidScope
	}
	if len(envelope) != protectedSecretBytes || envelope[0] != secretEnvelopeVersion {
		return "", ErrProtectedSecret
	}
	nonce := envelope[1 : 1+p.aead.NonceSize()]
	plaintext, err := p.aead.Open(nil, nonce, envelope[1+p.aead.NonceSize():], secretAdditionalData(scope))
	if err != nil || !canonicalSecret(string(plaintext)) {
		return "", ErrProtectedSecret
	}
	return string(plaintext), nil
}

func secretAdditionalData(scope protocol.Scope) []byte {
	// IDs are validated, fixed-width lowercase hexadecimal, so concatenation is
	// unambiguous. The domain also binds the envelope version and purpose.
	return []byte("ServerSend/TOTP-secret/v1\x00" + string(scope.ServerID) + string(scope.AccountID))
}

func canonicalSecret(secret string) bool {
	if len(secret) != 32 {
		return false
	}
	decoded, err := base32.StdEncoding.DecodeString(secret)
	return err == nil && len(decoded) == secretBytes && base32.StdEncoding.EncodeToString(decoded) == secret
}

// Pending passwords use a separate authenticated purpose from TOTP secrets.
func (p *SecretProtector) SealPassword(scope protocol.Scope, password string) ([]byte, error) {
	if p == nil || p.aead == nil || scope.Validate() != nil {
		return nil, ErrProtectedSecret
	}
	nonce := make([]byte, p.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return p.aead.Seal(nonce, nonce, []byte(password), []byte("ServerSend/pending-password/v1\x00"+string(scope.ServerID)+string(scope.AccountID))), nil
}
func (p *SecretProtector) OpenPassword(scope protocol.Scope, data []byte) (string, error) {
	if p == nil || p.aead == nil || scope.Validate() != nil || len(data) < p.aead.NonceSize()+p.aead.Overhead() {
		return "", ErrProtectedSecret
	}
	n := p.aead.NonceSize()
	plain, err := p.aead.Open(nil, data[:n], data[n:], []byte("ServerSend/pending-password/v1\x00"+string(scope.ServerID)+string(scope.AccountID)))
	return string(plain), err
}
