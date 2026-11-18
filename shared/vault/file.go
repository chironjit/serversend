package vault

import (
	"encoding/json"
	"errors"
	"github.com/chironjit/serversend/shared/protocol"
)

// Binding is authenticated for every object. Kind separates key, metadata,
// index and chunk uses; Index binds objects to their exact ordered position.
type Binding struct {
	protocol.Scope
	FileID    protocol.ID
	VersionID protocol.ID
	Kind      string
	Index     int64
}

func (b Binding) Bytes() []byte { return aad([]any{Format, b}) }

type Metadata struct {
	SHA256     protocol.Digest  `json:"sha256"`
	Name       string           `json:"name"`
	Bytes      int64            `json:"bytes"`
	WrappedKey []byte           `json:"wrapped_key"`
	Chunks     []protocol.Chunk `json:"chunks"`
}

func FileEnvelope(master, fileKey Key, m protocol.Manifest, name string, size int64, digest protocol.Digest) (*protocol.PrivateEnvelope, error) {
	if protocol.ValidateName(name) != nil || digest.Validate() != nil {
		return nil, errors.New("invalid private name")
	}
	b := Binding{Scope: m.Scope, FileID: m.FileID, VersionID: m.VersionID, Kind: "key"}
	meta := Metadata{SHA256: digest, Name: name, Bytes: size, WrappedKey: Seal(master, fileKey[:], b.Bytes()), Chunks: m.Chunks}
	b.Kind = "manifest"
	raw, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	return &protocol.PrivateEnvelope{Format: Format, Payload: Seal(master, raw, b.Bytes())}, nil
}
func ReadMetadata(master Key, m protocol.Manifest) (Metadata, Key, error) {
	var meta Metadata
	if m.Private == nil || m.Private.Format != Format {
		return meta, Key{}, errors.New("unsupported private format")
	}
	b := Binding{Scope: m.Scope, FileID: m.FileID, VersionID: m.VersionID, Kind: "manifest"}
	plain, err := Open(master, m.Private.Payload, b.Bytes())
	if err != nil {
		return meta, Key{}, err
	}
	defer clear(plain)
	if err = json.Unmarshal(plain, &meta); err != nil {
		return meta, Key{}, err
	}
	a, _ := json.Marshal(meta.Chunks)
	c, _ := json.Marshal(m.Chunks)
	if string(a) != string(c) || protocol.ValidateName(meta.Name) != nil || meta.Bytes < 0 || meta.SHA256.Validate() != nil {
		return meta, Key{}, errors.New("private manifest mismatch")
	}
	b.Kind = "key"
	k, err := keyFrom(Open(master, meta.WrappedKey, b.Bytes()))
	return meta, k, err
}
