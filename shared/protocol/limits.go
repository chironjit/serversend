package protocol

import "errors"

// TransferLimits describes server-enforced bounds in transmitted bytes. Clients
// must also apply their own resource budgets before allocating any buffers.
type TransferLimits struct {
	MinChunkBytes    int64 `json:"min_chunk_bytes"`
	MaxChunkBytes    int64 `json:"max_chunk_bytes"`
	MaxRequestBytes  int64 `json:"max_request_bytes"`
	MaxManifestBytes int64 `json:"max_manifest_bytes"`
	MaxChunks        int64 `json:"max_chunks"`
}

func (l TransferLimits) Validate() error {
	if l.MinChunkBytes <= 0 || l.MaxChunkBytes < l.MinChunkBytes {
		return errors.New("chunk limits must be positive and ordered")
	}
	if l.MaxManifestBytes <= 0 || l.MaxChunks <= 0 {
		return errors.New("manifest limits must be positive")
	}
	if l.MaxRequestBytes < l.MaxChunkBytes || l.MaxRequestBytes < l.MaxManifestBytes {
		return errors.New("request limit must accommodate chunks and manifests")
	}
	return nil
}

// ValidateChunkSize permits a final chunk shorter than MinChunkBytes. Empty
// files have zero chunks, rather than a zero-byte chunk. It validates only the
// byte bound; the upload state must separately verify which chunk is final.
func (l TransferLimits) ValidateChunkSize(bytes int64, final bool) error {
	if err := l.Validate(); err != nil {
		return err
	}
	if bytes <= 0 || bytes > l.MaxChunkBytes || (!final && bytes < l.MinChunkBytes) {
		return errors.New("chunk size outside negotiated bounds")
	}
	return nil
}

// ValidateManifestBounds checks encoded body size and chunk count before any
// manifest contents are trusted. It does not validate a cryptographic format.
func (l TransferLimits) ValidateManifestBounds(bytes, chunks int64) error {
	if err := l.Validate(); err != nil {
		return err
	}
	if bytes <= 0 || bytes > l.MaxManifestBytes || chunks < 0 || chunks > l.MaxChunks {
		return errors.New("manifest outside negotiated bounds")
	}
	return nil
}
