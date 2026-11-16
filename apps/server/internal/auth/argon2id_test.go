package auth

import (
	"strings"
	"testing"
)

func TestArgon2idHashesAreSaltedSelfDescribingAndBounded(t *testing.T) {
	fast := Argon2id{memoryKiB: 64, iterations: 1, parallelism: 2}
	first, err := fast.Hash("correct horse battery 密碼")
	if err != nil {
		t.Fatal(err)
	}
	second, err := fast.Hash("correct horse battery 密碼")
	if err != nil || first == second {
		t.Fatal("hashes are not independently salted")
	}
	if !strings.HasPrefix(first, "$argon2id$v=19$m=64,t=1,p=2$") || strings.Contains(first, "correct") {
		t.Fatalf("unexpected encoding: %s", first)
	}
	// Any instance verifies by the stored parameters, so defaults can change.
	if !NewArgon2id().Verify(first, "correct horse battery 密碼") || NewArgon2id().Verify(first, "correct horse battery") || fast.Verify(first, "") {
		t.Fatal("verification disagrees with the stored hash")
	}
	salt, key := strings.Split(first, "$")[4], strings.Split(first, "$")[5]
	for _, encoded := range []string{
		"", "plain", first + "$", first[1:], strings.Replace(first, "argon2id", "argon2i", 1), strings.Replace(first, "v=19", "v=16", 1),
		"$argon2id$v=19$m=64,t=1$" + salt + "$" + key, "$argon2id$v=19$t=1,m=64,p=2$" + salt + "$" + key,
		"$argon2id$v=19$m=064,t=1,p=2$" + salt + "$" + key, "$argon2id$v=19$m=64,t=0,p=2$" + salt + "$" + key,
		"$argon2id$v=19$m=8,t=1,p=2$" + salt + "$" + key, "$argon2id$v=19$m=64,t=1,p=0$" + salt + "$" + key,
		"$argon2id$v=19$m=4294967295,t=1,p=2$" + salt + "$" + key, "$argon2id$v=19$m=64,t=4294967295,p=2$" + salt + "$" + key,
		"$argon2id$v=19$m=64,t=1,p=255$" + salt + "$" + key, "$argon2id$v=19$m=64,t=1,p=2$" + salt[:8] + "$" + key,
		"$argon2id$v=19$m=64,t=1,p=2$" + salt + "$" + key[:20], "$argon2id$v=19$m=64,t=1,p=2$" + salt + "=$" + key,
		"$argon2id$v=19$m=64,t=1,p=2$" + salt + "$" + strings.Repeat("A", 300),
	} {
		if fast.Verify(encoded, "correct horse battery 密碼") {
			t.Fatalf("malformed or hostile hash matched: %q", encoded)
		}
	}
	for _, invalid := range []Argon2id{{}, {memoryKiB: 8, iterations: 1, parallelism: 2}, {memoryKiB: 1 << 30, iterations: 1, parallelism: 1}, {memoryKiB: 64, iterations: 11, parallelism: 1}} {
		if _, err := invalid.Hash("password"); err == nil {
			t.Fatalf("invalid parameters accepted: %+v", invalid)
		}
	}
}

func TestArgon2idProductionParameters(t *testing.T) {
	hasher := NewArgon2id()
	encoded, err := hasher.Hash("a long permanent password")
	if err != nil || !strings.HasPrefix(encoded, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("production hash: %s %v", encoded, err)
	}
	if !hasher.Verify(encoded, "a long permanent password") || hasher.Verify(encoded, "a long permanent passworD") {
		t.Fatal("production verification failed")
	}
}
