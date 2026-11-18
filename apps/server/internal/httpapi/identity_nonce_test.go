package httpapi

import (
	"context"
	"testing"

	"github.com/chironjit/serversend/shared/protocol"
)

func TestIdentityNoncesConsumedBeforeSigning(t *testing.T) {
	calls := 0
	var config options
	WithIdentity(func(context.Context, *protocol.IdentityProof) error { calls++; return nil })(&config)
	p := &protocol.IdentityProof{IdentityChallenge: protocol.IdentityChallenge{Nonce: "fresh-nonce"}}
	if err := config.signIdentity(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := config.signIdentity(t.Context(), p); err == nil {
		t.Fatal("nonce replay accepted")
	}
	if calls != 1 {
		t.Fatal("replayed challenge reached signer")
	}
	p.Nonce = "another-nonce"
	if err := config.signIdentity(t.Context(), p); err != nil || calls != 2 {
		t.Fatal("fresh challenge rejected", err)
	}
}
