package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

const testID ID = "11111111111111111111111111111111"

func TestIdentifiersAndScopedCollisions(t *testing.T) {
	for _, id := range []ID{testID, "00000000000000000000000000000000", "abcdef0123456789abcdef0123456789"} {
		if err := id.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []ID{"", "../file", ID(strings.Repeat("a", 31)), ID(strings.Repeat("a", 33)), ID(strings.Repeat("A", 32)), ID(strings.Repeat("g", 32))} {
		if id.Validate() == nil {
			t.Fatalf("invalid identifier accepted: %q", id)
		}
	}
	keys := []ResourceKey{
		{Scope{testID, testID}, testID},
		{Scope{"22222222222222222222222222222222", testID}, testID},
		{Scope{testID, "22222222222222222222222222222222"}, testID},
	}
	cache := make(map[ResourceKey]string)
	for i, key := range keys {
		if err := key.Validate(); err != nil {
			t.Fatal(err)
		}
		cache[key] = string(rune('a' + i))
	}
	if len(cache) != 3 {
		t.Fatal("colliding IDs crossed server/account boundaries")
	}
	for _, key := range []ResourceKey{{Scope{"", testID}, testID}, {Scope{testID, ""}, testID}, {Scope{testID, testID}, ""}} {
		if key.Validate() == nil {
			t.Fatal("incomplete scope accepted")
		}
	}
}

func TestRevisionWirePrecisionAndCanonicalForm(t *testing.T) {
	for _, revision := range []Revision{"0", "1", "9007199254740993", "18446744073709551615"} {
		if err := revision.Validate(); err != nil {
			t.Fatal(err)
		}
		wire, err := json.Marshal(revision)
		if err != nil || string(wire) != `"`+string(revision)+`"` {
			t.Fatalf("revision lost precision: %s, %v", wire, err)
		}
	}
	for _, revision := range []Revision{"", "01", "-1", "+1", "1.0", " 1", "1e2", "18446744073709551616"} {
		if revision.Validate() == nil {
			t.Fatalf("invalid revision accepted: %q", revision)
		}
	}
	var revision Revision
	if json.Unmarshal([]byte(`1`), &revision) == nil {
		t.Fatal("numeric revision accepted")
	}
	for _, tc := range []struct {
		a, b Revision
		want int
	}{{"9", "10", -1}, {"10", "9", 1}, {"9007199254740993", "9007199254740993", 0}} {
		if got, err := CompareRevisions(tc.a, tc.b); err != nil || got != tc.want {
			t.Fatalf("revision comparison=%d, %v", got, err)
		}
	}
	if _, err := CompareRevisions("01", "1"); err == nil {
		t.Fatal("invalid comparison operand accepted")
	}
}

func TestDigestCanonicalForm(t *testing.T) {
	for _, value := range []Digest{Digest(strings.Repeat("a", 64)), Digest(strings.Repeat("0", 64))} {
		if err := value.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []Digest{"", Digest(strings.Repeat("a", 63)), Digest(strings.Repeat("A", 64)), Digest(strings.Repeat("x", 64))} {
		if value.Validate() == nil {
			t.Fatal("invalid digest accepted")
		}
	}
}
