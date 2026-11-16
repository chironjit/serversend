package twofactor

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/chironjit/serversend/shared/protocol"
)

var protectionScope = protocol.Scope{
	ServerID:  "0123456789abcdef0123456789abcdef",
	AccountID: "abcdef0123456789abcdef0123456789",
}

const protectionSecret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"

func testProtector(t *testing.T) *SecretProtector {
	t.Helper()
	p, err := NewSecretProtector(bytes.Repeat([]byte{0xa3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSecretProtectionRoundTripAndFreshNonce(t *testing.T) {
	p := testProtector(t)
	seen := make(map[string]bool)
	for range 100 {
		envelope, err := p.Seal(protectionScope, protectionSecret)
		if err != nil || len(envelope) != protectedSecretBytes {
			t.Fatal("secret protection failed")
		}
		if bytes.Contains(envelope, []byte(protectionSecret)) {
			t.Fatal("protected secret contains plaintext")
		}
		if seen[string(envelope)] {
			t.Fatal("secret protection reused a nonce")
		}
		seen[string(envelope)] = true
		got, err := p.Open(protectionScope, envelope)
		if err != nil || got != protectionSecret {
			t.Fatal("protected secret did not round trip")
		}
		// Serialization is self-contained; a fresh instance with the retained
		// deployment key can decrypt after an ordinary process restart.
		got, err = testProtector(t).Open(protectionScope, bytes.Clone(envelope))
		if err != nil || got != protectionSecret {
			t.Fatal("fresh protector could not open retained envelope")
		}
	}
}

func TestSecretProtectionRejectsTamperingAndForeignContext(t *testing.T) {
	p := testProtector(t)
	envelope, err := p.Seal(protectionScope, protectionSecret)
	if err != nil {
		t.Fatal(err)
	}
	for i := range envelope {
		modified := bytes.Clone(envelope)
		modified[i] ^= 1
		if got, err := p.Open(protectionScope, modified); err == nil || got != "" {
			t.Fatalf("modified envelope byte %d accepted", i)
		}
	}
	for _, scope := range []protocol.Scope{
		{ServerID: "22222222222222222222222222222222", AccountID: protectionScope.AccountID},
		{ServerID: protectionScope.ServerID, AccountID: "33333333333333333333333333333333"},
	} {
		if got, err := p.Open(scope, envelope); !errors.Is(err, ErrProtectedSecret) || got != "" {
			t.Fatal("protected secret moved across account/server boundary")
		}
	}
	wrongKey, err := NewSecretProtector(bytes.Repeat([]byte{0xb4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := wrongKey.Open(protectionScope, envelope); !errors.Is(err, ErrProtectedSecret) || got != "" {
		t.Fatal("wrong protection key accepted")
	}
	for _, malformed := range [][]byte{nil, {}, {1}, envelope[:12], envelope[:len(envelope)-1], append(bytes.Clone(envelope), 0), bytes.Repeat([]byte{1}, 1024)} {
		if got, err := p.Open(protectionScope, malformed); !errors.Is(err, ErrProtectedSecret) || got != "" {
			t.Fatal("malformed envelope returned plaintext")
		}
	}
}

func TestSecretProtectionValidatesInputs(t *testing.T) {
	for _, size := range []int{0, 16, 24, 31, 33, 64} {
		if p, err := NewSecretProtector(make([]byte, size)); !errors.Is(err, ErrProtectionKey) || p != nil {
			t.Fatalf("key length %d accepted", size)
		}
	}
	p := testProtector(t)
	for _, secret := range []string{"", protectionSecret[:31], protectionSecret + "A", strings.ToLower(protectionSecret), strings.Repeat("!", 32), strings.Repeat("A", 31) + "="} {
		if result, err := p.Seal(protectionScope, secret); !errors.Is(err, ErrInvalidSecret) || result != nil {
			t.Fatal("invalid TOTP secret accepted for protection")
		}
	}
	for _, scope := range []protocol.Scope{{}, {ServerID: protectionScope.ServerID, AccountID: "INVALID"}, {ServerID: "invalid", AccountID: protectionScope.AccountID}} {
		if result, err := p.Seal(scope, protectionSecret); !errors.Is(err, ErrInvalidScope) || result != nil {
			t.Fatal("invalid secret protection scope accepted")
		}
		if secret, err := p.Open(scope, nil); !errors.Is(err, ErrInvalidScope) || secret != "" {
			t.Fatal("invalid secret opening scope accepted")
		}
	}
	for _, zero := range []*SecretProtector{nil, {}} {
		if result, err := zero.Seal(protectionScope, protectionSecret); !errors.Is(err, ErrProtectedSecret) || result != nil {
			t.Fatal("uninitialized protector accepted")
		}
		if secret, err := zero.Open(protectionScope, nil); !errors.Is(err, ErrProtectedSecret) || secret != "" {
			t.Fatal("uninitialized protector returned plaintext")
		}
	}
}

func TestSecretProtectionEntropyFailuresReturnNoEnvelope(t *testing.T) {
	p := testProtector(t)
	for _, random := range []io.Reader{bytes.NewReader(nil), bytes.NewReader(make([]byte, 11))} {
		if result, err := p.seal(protectionScope, protectionSecret, random); !errors.Is(err, ErrProtectedSecret) || result != nil {
			t.Fatal("entropy failure returned a partial protected secret")
		}
	}
}

func TestSecretProtectorConcurrentUse(t *testing.T) {
	p := testProtector(t)
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			envelope, err := p.Seal(protectionScope, protectionSecret)
			if err != nil {
				t.Error("concurrent protection failed")
				return
			}
			secret, err := p.Open(protectionScope, envelope)
			if err != nil || secret != protectionSecret {
				t.Error("concurrent opening failed")
			}
		})
	}
	group.Wait()
}

func FuzzProtectedSecretEnvelope(f *testing.F) {
	p, err := NewSecretProtector(bytes.Repeat([]byte{0xa3}, 32))
	if err != nil {
		f.Fatal(err)
	}
	envelope, err := p.Seal(protectionScope, protectionSecret)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(envelope)
	f.Add([]byte{})
	f.Add([]byte{1})
	f.Fuzz(func(t *testing.T, input []byte) {
		secret, err := p.Open(protectionScope, input)
		if err != nil && secret != "" {
			t.Fatal("invalid envelope returned partial plaintext")
		}
		if err == nil && secret != protectionSecret {
			t.Fatal("forged envelope accepted")
		}
	})
}
