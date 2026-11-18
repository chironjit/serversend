package vault

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"github.com/chironjit/serversend/shared/protocol"
	"testing"
)

var testScope = protocol.Scope{ServerID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AccountID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}

func TestVaultRecoveryAndRewrap(t *testing.T) {
	e, r, k, err := Create(testScope, "a strong separate passphrase")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(e)
	if bytes.Contains(raw, []byte("a strong")) || bytes.Contains(raw, k[:]) || bytes.Contains(raw, r.Secret[:]) {
		t.Fatal("secret in envelope")
	}
	recovered, err := e.Recover(r)
	if err != nil || recovered != k {
		t.Fatal("clean device recovery", err)
	}
	unlocked, err := e.Unlock("a strong separate passphrase")
	if err != nil || unlocked != k {
		t.Fatal(err)
	}
	if _, err = e.Unlock("wrong password"); err == nil {
		t.Fatal("wrong passphrase accepted")
	}
	next, err := e.Rewrap(k, "a different strong passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if next.Revision != 2 {
		t.Fatal("revision")
	}
	if _, err = next.Unlock("a strong separate passphrase"); err == nil {
		t.Fatal("old passphrase")
	}
	if got, err := next.Recover(r); err != nil || got != k {
		t.Fatal("recovery changed", err)
	}
	e.Memory = 1 << 31
	if _, err = e.Unlock("anything"); err == nil {
		t.Fatal("unbounded KDF accepted")
	}
	r.AccountID = testScope.ServerID
	if _, err = next.Recover(r); err == nil {
		t.Fatal("scope swap")
	}
}
func TestNISTEmptyAES256GCMVector(t *testing.T) {
	// NIST AES-256-GCM zero key, zero 96-bit nonce, empty plaintext/AAD.
	cipher, _ := hex.DecodeString("000000000000000000000000530f8afbc74536b9a963b4f1c4cb738b")
	p, err := Open(Key{}, cipher, nil)
	if err != nil || len(p) != 0 {
		t.Fatal(err)
	}
}
func TestPrivateManifestAndAdversaries(t *testing.T) {
	master, fk := RandomKey(), RandomKey()
	m := protocol.Manifest{SchemaVersion: 1, Scope: testScope, FileID: RandomID(), VersionID: RandomID(), Mode: protocol.Private, Chunks: []protocol.Chunk{}}
	b := Binding{Scope: m.Scope, FileID: m.FileID, VersionID: m.VersionID, Kind: "chunk"}
	data := Seal(fk, []byte("plaintext canary"), b.Bytes())
	second := Seal(fk, []byte("plaintext canary"), b.Bytes())
	if bytes.Equal(data, second) {
		t.Fatal("fresh nonce not generated")
	}
	m.Chunks = []protocol.Chunk{{Index: 0, Bytes: int64(len(data)), SHA256: protocol.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}}
	m.TotalBytes = int64(len(data))
	m.Private, _ = FileEnvelope(master, fk, m, "secret-name", 16, protocol.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	meta, got, err := ReadMetadata(master, m)
	if err != nil || got != fk || meta.Name != "secret-name" {
		t.Fatal(err)
	}
	for _, kind := range []string{"tamper", "truncate", "duplicate", "position", "scope", "key"} {
		t.Run(kind, func(t *testing.T) {
			d := append([]byte{}, data...)
			bound := b
			k := fk
			switch kind {
			case "tamper":
				d[len(d)-1] ^= 1
			case "truncate":
				d = d[:len(d)-1]
			case "duplicate":
				d = append(d, d...)
			case "position":
				bound.Index = 1
			case "scope":
				bound.AccountID = testScope.ServerID
			case "key":
				k = master
			}
			if _, err := Open(k, d, bound.Bytes()); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	m.Chunks = append(m.Chunks, m.Chunks[0])
	if _, _, err = ReadMetadata(master, m); err == nil {
		t.Fatal("manifest duplication accepted")
	}
	m.Chunks = m.Chunks[:1]
	m.VersionID = RandomID()
	if _, _, err = ReadMetadata(master, m); err == nil {
		t.Fatal("version swap")
	}
}
func BenchmarkKDF(b *testing.B) {
	for b.Loop() {
		k := derive("benchmark vault passphrase", make([]byte, 16))
		clear(k[:])
	}
}
func BenchmarkObject1KiB(b *testing.B) {
	k := RandomKey()
	p := make([]byte, 1024)
	b.SetBytes(1024)
	b.ReportAllocs()
	for b.Loop() {
		Seal(k, p, []byte("benchmark"))
	}
}

func TestBindingVector(t *testing.T) {
	b := Binding{Scope: testScope, FileID: "cccccccccccccccccccccccccccccccc", VersionID: "dddddddddddddddddddddddddddddddd", Kind: "chunk", Index: 7}
	const expected = `["serversend-private-v1",{"server_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","account_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","FileID":"cccccccccccccccccccccccccccccccc","VersionID":"dddddddddddddddddddddddddddddddd","Kind":"chunk","Index":7}]`
	if string(b.Bytes()) != expected {
		t.Fatal("version 1 associated data changed", string(b.Bytes()))
	}
}
func FuzzObjectOpen(f *testing.F) {
	f.Add([]byte("malformed"))
	f.Add(make([]byte, 28))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			t.Skip()
		}
		_, _ = Open(Key{}, data, []byte("bound"))
	})
}
