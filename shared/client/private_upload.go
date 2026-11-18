package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/vault"
	"io"
	"os"
	"path/filepath"
)

// Private staging is one bounded file per active worker (maximum 1 GiB).
// It contains only ciphertext. The durable checkpoint is saved after fsync and
// before admission. A lost preparation is disposable; a resumed attempt never
// encrypts again under an existing immutable version ID.
const MaxPrivateStage = 1 << 30

func (c *Client) UploadPrivateResumable(ctx context.Context, path, stage string, parent protocol.ID, cp *UploadCheckpoint, save func(*UploadCheckpoint) error, progress Progress) (api.FinalizeResult, error) {
	if cp == nil || save == nil {
		return api.FinalizeResult{}, errors.New("private checkpoint and persistence callback are required")
	}
	original, err := filepath.Abs(path)
	if err != nil {
		return api.FinalizeResult{}, err
	}
	staged, err := filepath.Abs(stage)
	if err != nil {
		return api.FinalizeResult{}, err
	}
	if original == staged {
		return api.FinalizeResult{}, errors.New("private staging must be separate from the source")
	}
	k, err := c.VaultKey()
	if err != nil {
		return api.FinalizeResult{}, err
	}
	defer clear(k[:])
	limits, err := c.transferLimits(ctx)
	if err != nil {
		return api.FinalizeResult{}, err
	}
	if cp.Manifest.FileID == "" {
		src, info, err := openUploadSource(path)
		if err != nil {
			return api.FinalizeResult{}, err
		}
		defer src.Close()
		if info.Size() > MaxPrivateStage-(MaxPrivateStage/(1<<20)+1)*28 {
			return api.FinalizeResult{}, errors.New("private file exceeds the 1 GiB encrypted staging limit")
		}
		// Client-assisted name matching preserves IDs and revision conflict semantics.
		a, _ := c.Account()
		m := protocol.Manifest{SchemaVersion: 1, Scope: protocol.Scope{ServerID: a.ServerID, AccountID: a.AccountID}, FileID: randomID(), VersionID: randomID(), Mode: protocol.Private, Chunks: []protocol.Chunk{}}
		expected := protocol.Revision("0")
		for cursor := api.Cursor(""); ; {
			p, e := c.CatalogFolderPage(ctx, parent, cursor)
			if e != nil {
				return api.FinalizeResult{}, e
			}
			for _, entry := range p.Entries {
				if entry.Mode == protocol.Private {
					n, e := c.PrivateName(ctx, entry)
					if e != nil {
						return api.FinalizeResult{}, e
					}
					if n == filepath.Base(path) {
						if entry.Kind != api.File {
							return api.FinalizeResult{}, errors.New("private folder uses this name")
						}
						if expected != "0" {
							return api.FinalizeResult{}, errors.New("duplicate private names; resolve by choosing or removing an opaque entry")
						}
						m.FileID = entry.ID
						expected = entry.HeadRevision
					}
				}
			}
			if p.NextCursor == "" {
				break
			}
			cursor = p.NextCursor
		}
		out, err := os.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if os.IsExist(err) {
			if e := os.Remove(stage); e != nil {
				return api.FinalizeResult{}, e
			}
			out, err = os.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		}
		if err != nil {
			return api.FinalizeResult{}, err
		}
		defer out.Close()
		fk := vault.RandomKey()
		defer clear(fk[:])
		buffer := make([]byte, min(int64(preferredChunkBytes)-28, max(info.Size(), 1)))
		hasher := sha256.New()
		var size int64
		for {
			if err = ctx.Err(); err != nil {
				return api.FinalizeResult{}, err
			}
			if _, err = c.VaultKey(); err != nil {
				return api.FinalizeResult{}, err
			}
			n, e := io.ReadFull(src, buffer)
			if n > 0 {
				size += int64(n)
				hasher.Write(buffer[:n])
				b := vault.Binding{Scope: m.Scope, FileID: m.FileID, VersionID: m.VersionID, Kind: "chunk", Index: int64(len(m.Chunks))}
				data := vault.Seal(fk, buffer[:n], b.Bytes())
				sum := sha256.Sum256(data)
				m.Chunks = append(m.Chunks, protocol.Chunk{Index: b.Index, Bytes: int64(len(data)), SHA256: protocol.Digest(hex.EncodeToString(sum[:]))})
				m.TotalBytes += int64(len(data))
				if _, err = out.Write(data); err != nil {
					return api.FinalizeResult{}, err
				}
			}
			if e == io.EOF || e == io.ErrUnexpectedEOF {
				break
			}
			if e != nil {
				return api.FinalizeResult{}, e
			}
		}
		clear(buffer)
		now, e := src.Stat()
		if e != nil {
			return api.FinalizeResult{}, e
		}
		if size != info.Size() || now.Size() != size || !now.ModTime().Equal(info.ModTime()) {
			return api.FinalizeResult{}, ErrSourceChanged
		}
		m.Private, err = vault.FileEnvelope(k, fk, m, filepath.Base(path), size, protocol.Digest(hex.EncodeToString(hasher.Sum(nil))))
		if err != nil {
			return api.FinalizeResult{}, err
		}
		if err = m.Validate(limits, vault.Formats); err != nil {
			return api.FinalizeResult{}, err
		}
		body, _ := json.Marshal(m)
		if int64(len(body)) > limits.MaxManifestBytes {
			return api.FinalizeResult{}, errors.New("private manifest exceeds server limit")
		}
		if err = out.Sync(); err != nil {
			return api.FinalizeResult{}, err
		}
		*cp = UploadCheckpoint{Manifest: m, Intent: api.CreateUploadRequest{ParentID: parent, FileID: m.FileID, VersionID: m.VersionID, Mode: protocol.Private, ExpectedHead: expected, ChunkCount: int64(len(m.Chunks)), TransmittedBytes: m.TotalBytes, ManifestBytes: int64(len(body))}, IdempotencyKey: string(randomID())}
		if err = save(cp); err != nil {
			return api.FinalizeResult{}, err
		}
	}
	a, ok := c.Account()
	if !ok || cp.Manifest.Scope != (protocol.Scope{ServerID: a.ServerID, AccountID: a.AccountID}) || cp.Intent.ParentID != parent {
		return api.FinalizeResult{}, errors.New("private checkpoint scope mismatch")
	}
	if err = cp.Manifest.Validate(limits, vault.Formats); err != nil {
		return api.FinalizeResult{}, err
	}
	if cp.Manifest.Mode != protocol.Private {
		return api.FinalizeResult{}, errors.New("expected a private checkpoint")
	}
	if err = cp.validate(limits); err != nil {
		return api.FinalizeResult{}, err
	}
	_, fk, err := vault.ReadMetadata(k, cp.Manifest)
	clear(fk[:])
	if err != nil {
		return api.FinalizeResult{}, err
	}
	if cp.Result != nil {
		return checkpointResult(cp)
	}
	if cp.UploadID == "" {
		if err = c.admitCheckpoint(ctx, cp, save); err != nil {
			return api.FinalizeResult{}, err
		}
	}
	var upload api.Upload
	err = retryTransfer(ctx, func() error {
		return c.exchange(ctx, call{method: "GET", path: "/uploads/" + string(cp.UploadID), token: c.token()}, nil, &upload)
	})
	if err != nil {
		var failure api.Error
		if errors.As(err, &failure) && (failure.Code == api.UploadGone || failure.Code == api.NotFound) {
			*cp = UploadCheckpoint{}
			if e := save(cp); e != nil {
				return api.FinalizeResult{}, e
			}
			return c.UploadPrivateResumable(ctx, path, stage, parent, cp, save, progress)
		}
		return api.FinalizeResult{}, err
	}
	if upload.State == protocol.UploadCommitted || upload.State == protocol.UploadConflicted {
		return c.finalizeCheckpoint(ctx, cp, save)
	}
	if upload.State == protocol.UploadExpired || upload.State == protocol.UploadCancelled {
		*cp = UploadCheckpoint{}
		if err = save(cp); err != nil {
			return api.FinalizeResult{}, err
		}
		return c.UploadPrivateResumable(ctx, path, stage, parent, cp, save, progress)
	}
	f, err := os.Open(stage)
	if err != nil {
		if os.IsNotExist(err) {
			return c.restartPrivate(ctx, cp, save)
		}
		return api.FinalizeResult{}, err
	}
	defer f.Close()
	capacity := int64(1)
	for _, chunk := range cp.Manifest.Chunks {
		capacity = max(capacity, chunk.Bytes)
	}
	buffer := make([]byte, capacity)
	for round := 0; round <= len(cp.Manifest.Chunks); round++ {
		var missing api.MissingChunks
		err = retryTransfer(ctx, func() error {
			return c.exchange(ctx, call{method: "GET", path: "/uploads/" + string(cp.UploadID) + "/missing", token: c.token()}, nil, &missing)
		})
		if err != nil {
			return api.FinalizeResult{}, err
		}
		if missing.Validate() != nil || missing.UploadID != cp.UploadID || missing.ChunkCount != int64(len(cp.Manifest.Chunks)) {
			return api.FinalizeResult{}, errors.New("invalid private missing-chunk response")
		}
		if len(missing.Missing) == 0 {
			return c.finalizeCheckpoint(ctx, cp, save)
		}
		for _, r := range missing.Missing {
			for i := r.Start; i < r.End; i++ {
				if _, err = c.VaultKey(); err != nil {
					return api.FinalizeResult{}, err
				}
				if err = c.sendCheckpointChunk(ctx, f, buffer, cp, i, save, progress); err != nil {
					if errors.Is(err, ErrSourceChanged) {
						return c.restartPrivate(ctx, cp, save)
					}
					return api.FinalizeResult{}, err
				}
			}
		}
	}
	return api.FinalizeResult{}, errors.New("private upload made no progress")
}

// A lost/corrupt local ciphertext stage can never be regenerated under the old
// version. Retire admission before allowing fresh source encryption.
func (c *Client) restartPrivate(ctx context.Context, cp *UploadCheckpoint, save func(*UploadCheckpoint) error) (api.FinalizeResult, error) {
	err := c.CancelUpload(ctx, cp.UploadID)
	var failure api.Error
	if errors.As(err, &failure) {
		if failure.Code == api.Conflict {
			return c.finalizeCheckpoint(ctx, cp, save)
		}
		if failure.Code == api.UploadGone || failure.Code == api.NotFound {
			err = nil
		}
	}
	if err != nil {
		return api.FinalizeResult{}, err
	}
	*cp = UploadCheckpoint{}
	if err = save(cp); err != nil {
		return api.FinalizeResult{}, err
	}
	return api.FinalizeResult{}, ErrSourceChanged
}
