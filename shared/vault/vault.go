// Package vault implements the versioned client-side private format. Secrets
// returned here must never be sent to the server or written outside secure storage.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/chironjit/serversend/shared/protocol"
	"golang.org/x/crypto/argon2"
)

const Format = "serversend-private-v1"

var Formats = []string{Format}
var ErrLocked = errors.New("unlock the private vault first")
var kdfGate sync.Mutex // at most one 64 MiB derivation per process

type Key [32]byte
type Wrapper struct {
	Salt       []byte `json:"salt"`
	Ciphertext []byte `json:"ciphertext"`
}
type Envelope struct {
	Check  []byte `json:"check"`
	Format string `json:"format"`
	protocol.Scope
	VaultID    protocol.ID `json:"vault_id"`
	Revision   uint64      `json:"revision"`
	Memory     uint32      `json:"memory_kib"`
	Time       uint32      `json:"time"`
	Threads    uint8       `json:"threads"`
	Passphrase Wrapper     `json:"passphrase"`
	Recovery   []byte      `json:"recovery"`
}
type Recovery struct {
	Format string `json:"format"`
	protocol.Scope
	VaultID protocol.ID `json:"vault_id"`
	Secret  Key         `json:"secret"`
}

func RandomKey() Key {
	var k Key
	_, err := rand.Read(k[:])
	if err != nil {
		panic(err)
	}
	return k
}
func RandomID() protocol.ID {
	var b [16]byte
	_, err := rand.Read(b[:])
	if err != nil {
		panic(err)
	}
	return protocol.ID(fmt.Sprintf("%x", b))
}
func aad(v any) []byte { b, _ := json.Marshal(v); return b }
func Seal(k Key, plain, associated []byte) []byte {
	block, _ := aes.NewCipher(k[:])
	a, _ := cipher.NewGCMWithRandomNonce(block)
	return a.Seal(nil, nil, plain, associated)
}
func Open(k Key, ciphertext, associated []byte) ([]byte, error) {
	block, _ := aes.NewCipher(k[:])
	a, _ := cipher.NewGCMWithRandomNonce(block)
	return a.Open(nil, nil, ciphertext, associated)
}
func (e Envelope) binding(kind string) []byte { return aad([]any{Format, e.Scope, e.VaultID, kind}) }
func (e Envelope) Validate() error {
	if e.Format != Format || e.Scope.Validate() != nil || e.VaultID.Validate() != nil || e.Revision == 0 || e.Revision > 1<<53 || e.Memory != 65536 || e.Time != 3 || e.Threads != 1 || len(e.Passphrase.Salt) != 16 || len(e.Passphrase.Ciphertext) != 60 || len(e.Recovery) != 60 || len(e.Check) != len(Format)+28 {
		return errors.New("unsupported or invalid vault envelope")
	}
	return nil
}
func derive(pass string, salt []byte) Key {
	kdfGate.Lock()
	defer kdfGate.Unlock()
	b := argon2.IDKey([]byte(pass), salt, 3, 65536, 1, 32)
	var k Key
	copy(k[:], b)
	clear(b)
	return k
}
func (e *Envelope) setPassphrase(master Key, pass string) error {
	if len(pass) < 12 || len(pass) > 1024 {
		return errors.New("use a vault passphrase of 12 to 1024 bytes")
	}
	e.Passphrase.Salt = make([]byte, 16)
	if _, err := rand.Read(e.Passphrase.Salt); err != nil {
		return err
	}
	k := derive(pass, e.Passphrase.Salt)
	defer clear(k[:])
	e.Passphrase.Ciphertext = Seal(k, master[:], e.binding("passphrase"))
	return nil
}
func Create(scope protocol.Scope, pass string) (Envelope, Recovery, Key, error) {
	e := Envelope{Format: Format, Scope: scope, VaultID: RandomID(), Revision: 1, Memory: 65536, Time: 3, Threads: 1}
	r := Recovery{Format: Format, Scope: scope, VaultID: e.VaultID, Secret: RandomKey()}
	master := RandomKey()
	if scope.Validate() != nil {
		return Envelope{}, Recovery{}, Key{}, errors.New("invalid vault scope")
	}
	if err := e.setPassphrase(master, pass); err != nil {
		return Envelope{}, Recovery{}, Key{}, err
	}
	e.Recovery = Seal(r.Secret, master[:], e.binding("recovery"))
	e.Check = Seal(master, []byte(Format), e.binding("check"))
	return e, r, master, nil
}
func keyFrom(b []byte, err error) (Key, error) {
	var k Key
	if err != nil || len(b) != 32 {
		clear(b)
		return k, errors.New("vault unlock failed")
	}
	copy(k[:], b)
	clear(b)
	return k, nil
}
func (e Envelope) Unlock(pass string) (Key, error) {
	if err := e.Validate(); err != nil {
		return Key{}, err
	}
	if len(pass) > 1024 {
		return Key{}, errors.New("passphrase too long")
	}
	k := derive(pass, e.Passphrase.Salt)
	defer clear(k[:])
	return e.confirm(keyFrom(Open(k, e.Passphrase.Ciphertext, e.binding("passphrase"))))
}
func (e Envelope) Recover(r Recovery) (Key, error) {
	if err := e.Validate(); err != nil {
		return Key{}, err
	}
	if r.Format != Format || r.Scope != e.Scope || r.VaultID != e.VaultID {
		return Key{}, errors.New("recovery package belongs to another vault")
	}
	return e.confirm(keyFrom(Open(r.Secret, e.Recovery, e.binding("recovery"))))
}
func (e Envelope) Rewrap(master Key, pass string) (Envelope, error) {
	if err := e.Validate(); err != nil {
		return e, err
	}
	if err := e.VerifyKey(master); err != nil {
		return e, err
	}
	e.Revision++
	err := e.setPassphrase(master, pass)
	return e, err
}

func (e Envelope) VerifyKey(master Key) error {
	if err := e.Validate(); err != nil {
		return err
	}
	p, err := Open(master, e.Check, e.binding("check"))
	defer clear(p)
	if err != nil || string(p) != Format {
		return errors.New("vault key confirmation failed")
	}
	return nil
}

func (e Envelope) confirm(k Key, err error) (Key, error) {
	if err == nil {
		err = e.VerifyKey(k)
	}
	if err != nil {
		clear(k[:])
		return Key{}, err
	}
	return k, nil
}
