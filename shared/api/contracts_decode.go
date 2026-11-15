package api

import (
	"errors"
	"io"

	"github.com/chironjit/serversend/shared/protocol"
)

// MaxControlBytes is a local parser ceiling, independent of advertised peer
// limits. Streaming chunk bodies never use a JSON decoder.
const MaxControlBytes = 1 << 20

func decodeValidated[T interface{ Validate() error }](r io.Reader, limit int64) (T, error) {
	var value T
	if err := decode(r, limit, &value); err != nil {
		var zero T
		return zero, err
	}
	if err := value.Validate(); err != nil {
		var zero T
		return zero, err
	}
	return value, nil
}

func DecodeCreateUpload(r io.Reader, limits protocol.TransferLimits) (CreateUploadRequest, error) {
	if err := limits.Validate(); err != nil {
		return CreateUploadRequest{}, err
	}
	var value CreateUploadRequest
	if err := decode(r, min(limits.MaxRequestBytes, MaxControlBytes), &value); err != nil {
		return CreateUploadRequest{}, err
	}
	if err := value.Validate(limits); err != nil {
		return CreateUploadRequest{}, err
	}
	return value, nil
}

func DecodeManifest(r io.Reader, limits protocol.TransferLimits, privateFormats []string) (protocol.Manifest, error) {
	if err := limits.Validate(); err != nil {
		return protocol.Manifest{}, err
	}
	limit := min(limits.MaxManifestBytes, MaxControlBytes)
	var value protocol.Manifest
	if err := decode(r, limit, &value); err != nil {
		return protocol.Manifest{}, err
	}
	if err := value.Validate(limits, privateFormats); err != nil {
		return protocol.Manifest{}, err
	}
	return value, nil
}

func DecodeUpload(r io.Reader, limits protocol.TransferLimits) (Upload, error) {
	var value Upload
	if err := decode(r, MaxControlBytes, &value); err != nil {
		return Upload{}, err
	}
	if err := value.Validate(limits); err != nil {
		return Upload{}, err
	}
	return value, nil
}

func DecodeMissingChunks(r io.Reader) (MissingChunks, error) {
	return decodeValidated[MissingChunks](r, MaxControlBytes)
}
func DecodeChunkAcknowledgement(r io.Reader) (ChunkAcknowledgement, error) {
	return decodeValidated[ChunkAcknowledgement](r, MaxEventBytes)
}
func DecodeFinalizeResult(r io.Reader) (FinalizeResult, error) {
	return decodeValidated[FinalizeResult](r, MaxEventBytes)
}
func DecodeIdentityChallenge(r io.Reader) (protocol.IdentityChallenge, error) {
	return decodeValidated[protocol.IdentityChallenge](r, MaxEventBytes)
}
func DecodeIdentityProof(r io.Reader) (protocol.IdentityProof, error) {
	return decodeValidated[protocol.IdentityProof](r, MaxEventBytes)
}
func DecodeEvent(r io.Reader) (Event, error) { return decodeValidated[Event](r, MaxEventBytes) }
func DecodeVersionPage(r io.Reader) (VersionPage, error) {
	return decodeValidated[VersionPage](r, MaxControlBytes)
}

func DecodeCatalogChanges(r io.Reader, privateFormats []string) (CatalogChanges, error) {
	var value CatalogChanges
	if err := decode(r, MaxControlBytes, &value); err != nil {
		return CatalogChanges{}, err
	}
	if err := value.Validate(privateFormats); err != nil {
		return CatalogChanges{}, err
	}
	return value, nil
}

func DecodeCatalogPage(r io.Reader, privateFormats []string) (CatalogPage, error) {
	var value CatalogPage
	if err := decode(r, MaxControlBytes, &value); err != nil {
		return CatalogPage{}, err
	}
	if err := value.Validate(privateFormats); err != nil {
		return CatalogPage{}, err
	}
	return value, nil
}

// ValidateManifestBodyLength is checked against the received byte count, not
// re-encoded JSON, because whitespace and additive fields change body size.
func ValidateManifestBodyLength(received int64, u Upload) error {
	if received <= 0 || received != u.ManifestBytes {
		return errors.New("manifest body length differs from upload intent")
	}
	return nil
}
