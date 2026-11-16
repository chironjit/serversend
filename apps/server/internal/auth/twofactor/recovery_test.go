package twofactor

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/chironjit/serversend/shared/protocol"
)

func TestRecoveryCodesGenerateDistinctScopedHashes(t *testing.T) {
	seen := make(map[[sha256.Size]byte]bool)
	for range 2 {
		codes, err := GenerateRecoveryCodes(protectionScope)
		if err != nil || len(codes) != RecoveryCodeCount {
			t.Fatal("could not generate complete recovery code set")
		}
		for _, code := range codes {
			if len(code.Code) != 35 || strings.Count(code.Code, "-") != 3 || strings.ToUpper(code.Code) != code.Code {
				t.Fatal("recovery code has unexpected presentation")
			}
			if seen[code.Hash] {
				t.Fatal("recovery code generation repeated entropy")
			}
			seen[code.Hash] = true
			hash, err := HashRecoveryCode(protectionScope, code.Code)
			if err != nil || hash != code.Hash || !MatchRecoveryCode(protectionScope, code.Code, code.Hash) {
				t.Fatal("issued recovery code does not verify")
			}
		}
	}
}

func TestRecoveryCodeNormalizationAndScope(t *testing.T) {
	const code = "01234567-89ABCDEF-01234567-89ABCDEF"
	hash, err := HashRecoveryCode(protectionScope, code)
	if err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{code, strings.ToLower(code), strings.ReplaceAll(code, "-", ""), " \t" + strings.ToLower(code) + "\n"} {
		if !MatchRecoveryCode(protectionScope, variant, hash) {
			t.Fatal("supported recovery code presentation rejected")
		}
	}
	for _, scope := range []protocol.Scope{
		{ServerID: "22222222222222222222222222222222", AccountID: protectionScope.AccountID},
		{ServerID: protectionScope.ServerID, AccountID: "33333333333333333333333333333333"},
	} {
		foreign, err := HashRecoveryCode(scope, code)
		if err != nil || foreign == hash || MatchRecoveryCode(scope, code, hash) {
			t.Fatal("recovery code hash crossed server/account boundary")
		}
	}
	if hash == sha256.Sum256([]byte(strings.ReplaceAll(code, "-", ""))) {
		t.Fatal("recovery code hash lacks domain and account binding")
	}
	wrong := hash
	wrong[0] ^= 1
	if MatchRecoveryCode(protectionScope, code, wrong) {
		t.Fatal("wrong recovery code hash accepted")
	}
}

func TestRecoveryCodeRejectsMalformedInput(t *testing.T) {
	for _, code := range []string{
		"", "1234", strings.Repeat("A", 31), strings.Repeat("A", 33), strings.Repeat("A", 65),
		"01234567 89ABCDEF 01234567 89ABCDEF", "0123456-789ABCDEF-01234567-89ABCDEF",
		"01234567--89ABCDEF-01234567-89ABCDEF", "01234567-89ABCDEG-01234567-89ABCDEF",
		"０１２３４５６７８９ＡＢＣＤＥＦ", "01234567\x0089ABCDEF0123456789ABCDEF",
	} {
		if hash, err := HashRecoveryCode(protectionScope, code); !errors.Is(err, ErrRecoveryCode) || hash != [sha256.Size]byte{} {
			t.Fatal("malformed recovery code accepted")
		}
		if MatchRecoveryCode(protectionScope, code, [sha256.Size]byte{}) {
			t.Fatal("malformed recovery code matched an empty digest")
		}
	}
	if hash, err := HashRecoveryCode(protocol.Scope{}, strings.Repeat("A", 32)); !errors.Is(err, ErrInvalidScope) || hash != [sha256.Size]byte{} {
		t.Fatal("invalid recovery code scope accepted")
	}
	if codes, err := GenerateRecoveryCodes(protocol.Scope{}); !errors.Is(err, ErrInvalidScope) || codes != nil {
		t.Fatal("invalid recovery generation scope accepted")
	}
}

func TestRecoveryCodeGenerationFailureReturnsNoPartialSet(t *testing.T) {
	for _, random := range []io.Reader{
		bytes.NewReader(nil), bytes.NewReader(make([]byte, recoveryCodeBytes-1)),
		bytes.NewReader(make([]byte, recoveryCodeBytes+1)),
		// Enough bytes but a broken source repeating the same code must fail
		// promptly instead of returning duplicates or retrying forever.
		bytes.NewReader(make([]byte, recoveryCodeBytes*RecoveryCodeCount)),
	} {
		if codes, err := generateRecoveryCodes(protectionScope, random); !errors.Is(err, ErrRecoveryCodes) || codes != nil {
			t.Fatal("recovery entropy failure returned partial codes")
		}
	}
}

func FuzzRecoveryCodeNormalization(f *testing.F) {
	for _, input := range []string{"", "01234567-89ABCDEF-01234567-89ABCDEF", "0123456789abcdef0123456789abcdef", "\xff"} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		hash, err := HashRecoveryCode(protectionScope, input)
		if err != nil {
			if hash != [sha256.Size]byte{} || MatchRecoveryCode(protectionScope, input, hash) {
				t.Fatal("rejected code returned a usable digest")
			}
			return
		}
		if !MatchRecoveryCode(protectionScope, input, hash) {
			t.Fatal("valid code could not match its normalized hash")
		}
	})
}
