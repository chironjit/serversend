package protocol

import (
	"errors"
	"math"
	"strings"
	"unicode/utf8"
)

type StorageMode string

const (
	Standard         StorageMode = "standard"
	Private          StorageMode = "private"
	ManifestVersion1             = 1
)

func (m StorageMode) Validate() error {
	if m != Standard && m != Private {
		return errors.New("unknown storage mode")
	}
	return nil
}

type Chunk struct {
	Index  int64  `json:"index"`
	Bytes  int64  `json:"bytes"`
	SHA256 Digest `json:"sha256"`
}

func (c Chunk) Validate() error {
	if c.Index < 0 || c.Bytes <= 0 {
		return errors.New("chunk index or byte count is invalid")
	}
	return c.SHA256.Validate()
}

type StandardMetadata struct {
	Name string `json:"name"`
}

// ValidateName checks a single logical name. Download publication additionally
// requires destination-platform, containment and symlink checks.
func ValidateName(name string) error {
	if !utf8.ValidString(name) || len(name) == 0 || len(name) > 255 || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return errors.New("invalid logical name")
	}
	for _, c := range name {
		if c < 32 || c == 127 {
			return errors.New("control character in name")
		}
	}
	return nil
}

// PrivateEnvelope is an opaque extension point, not an approved cryptographic
// format. Chunk 17 must define its authenticated contents and review them.
type PrivateEnvelope struct {
	Format  string `json:"format"`
	Payload []byte `json:"payload"`
}

func (e PrivateEnvelope) Validate(supported []string) error {
	if len(e.Payload) == 0 {
		return errors.New("private envelope is empty")
	}
	for _, format := range supported {
		if format != "" && e.Format == format {
			return nil
		}
	}
	return errors.New("unsupported private format")
}

// Manifest describes immutable transmitted objects in exact order. The server
// validates these structural rules and stored objects, while only the desktop
// can authenticate a private envelope. Encoded size is checked before parsing.
type Manifest struct {
	SchemaVersion uint16 `json:"schema_version"`
	Scope
	FileID     ID                `json:"file_id"`
	VersionID  ID                `json:"version_id"`
	Mode       StorageMode       `json:"mode"`
	TotalBytes int64             `json:"total_bytes"`
	Chunks     []Chunk           `json:"chunks"`
	Standard   *StandardMetadata `json:"standard,omitempty"`
	Private    *PrivateEnvelope  `json:"private,omitempty"`
}

func (m Manifest) Validate(limits TransferLimits, privateFormats []string) error {
	if err := limits.Validate(); err != nil {
		return err
	}
	if m.SchemaVersion != ManifestVersion1 {
		return errors.New("unsupported manifest schema")
	}
	if err := m.Scope.Validate(); err != nil {
		return err
	}
	if err := m.FileID.Validate(); err != nil {
		return err
	}
	if err := m.VersionID.Validate(); err != nil {
		return err
	}
	if err := m.Mode.Validate(); err != nil {
		return err
	}
	if m.TotalBytes < 0 || m.Chunks == nil || int64(len(m.Chunks)) > limits.MaxChunks {
		return errors.New("invalid manifest totals or chunk list")
	}
	if m.Mode == Standard {
		if m.Standard == nil || m.Private != nil {
			return errors.New("standard manifest requires only standard metadata")
		}
		if err := ValidateName(m.Standard.Name); err != nil {
			return err
		}
	} else {
		if m.Private == nil || m.Standard != nil {
			return errors.New("private manifest requires only an opaque envelope")
		}
		if err := m.Private.Validate(privateFormats); err != nil {
			return err
		}
	}
	var total int64
	for i, chunk := range m.Chunks {
		if err := chunk.Validate(); err != nil {
			return err
		}
		if chunk.Index != int64(i) {
			return errors.New("manifest chunks must be contiguous and ordered from zero")
		}
		if err := limits.ValidateChunkSize(chunk.Bytes, i == len(m.Chunks)-1); err != nil {
			return err
		}
		if chunk.Bytes > math.MaxInt64-total {
			return errors.New("manifest length overflow")
		}
		total += chunk.Bytes
	}
	if total != m.TotalBytes {
		return errors.New("manifest length does not match objects")
	}
	return nil
}
