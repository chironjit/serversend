package client

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/source"
)

// ErrSourceChanged means the file was modified while it was being uploaded.
// Nothing was published; uploading again starts from the new contents.
var ErrSourceChanged = errors.New("the file changed while it was being uploaded; upload it again")

// Progress receives transmitted and total bytes. It is called from the
// transferring goroutine and must return quickly.
type Progress func(done, total int64)

const preferredChunkBytes = 4 << 20

func randomID() protocol.ID {
	value := make([]byte, 16)
	_, _ = rand.Read(value)
	return protocol.ID(hex.EncodeToString(value))
}

// Upload sends one regular file as a new standard-mode file. Memory use is one
// chunk buffer regardless of file size. The file is read twice: once to
// describe it and once to send it, and any difference aborts the upload.
func (c *Client) Upload(ctx context.Context, path string, progress Progress) (api.FinalizeResult, error) {
	return c.UploadTo(ctx, path, "", progress)
}

func (c *Client) UploadTo(ctx context.Context, path string, parent protocol.ID, progress Progress) (_ api.FinalizeResult, err error) {
	account, signedIn := c.Account()
	if !signedIn {
		return api.FinalizeResult{}, api.Error{Code: api.AuthenticationRequired, Message: "Sign in before uploading"}
	}
	name := filepath.Base(path)
	file, err := source.Open(path)
	if err != nil {
		return api.FinalizeResult{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return api.FinalizeResult{}, err
	}
	if !info.Mode().IsRegular() {
		return api.FinalizeResult{}, &source.SkipError{Path: path, Reason: "Directory skipped; use Upload folder to preserve its contents"}
	}
	if err := protocol.ValidateName(name); err != nil {
		return api.FinalizeResult{}, fmt.Errorf("%q cannot be used as a file name on the server", name)
	}
	var limits protocol.TransferLimits
	err = retryTransfer(ctx, func() error {
		var e error
		limits, e = c.transferLimits(ctx)
		return e
	})
	if err != nil {
		return api.FinalizeResult{}, err
	}
	chunkBytes := min(max(int64(preferredChunkBytes), limits.MinChunkBytes), limits.MaxChunkBytes)
	if info.Size()/chunkBytes >= limits.MaxChunks/2 || info.Size()/chunkBytes > limits.MaxManifestBytes/128 {
		chunkBytes = limits.MaxChunkBytes
	}
	// A small file needs only its own bytes, rather than a full chunk-sized
	// allocation. Keep one byte for the final growth check on empty files.
	buffer := make([]byte, min(chunkBytes, max(info.Size(), 1)))

	manifest := protocol.Manifest{SchemaVersion: protocol.ManifestVersion1, Scope: protocol.Scope{ServerID: account.ServerID, AccountID: account.AccountID},
		FileID: randomID(), VersionID: randomID(), Mode: protocol.Standard, Chunks: []protocol.Chunk{}, Standard: &protocol.StandardMetadata{Name: name}}
	for {
		n, err := io.ReadFull(file, buffer)
		if n > 0 {
			if int64(n) > info.Size()-manifest.TotalBytes {
				return api.FinalizeResult{}, ErrSourceChanged
			}
			sum := sha256.Sum256(buffer[:n])
			manifest.Chunks = append(manifest.Chunks, protocol.Chunk{Index: int64(len(manifest.Chunks)), Bytes: int64(n), SHA256: protocol.Digest(hex.EncodeToString(sum[:]))})
			manifest.TotalBytes += int64(n)
			if int64(len(manifest.Chunks)) > limits.MaxChunks {
				return api.FinalizeResult{}, fmt.Errorf("%q is larger than this server version accepts", name)
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return api.FinalizeResult{}, err
		}
		if err := ctx.Err(); err != nil {
			return api.FinalizeResult{}, err
		}
	}
	currentInfo, err := file.Stat()
	if err != nil {
		return api.FinalizeResult{}, err
	}
	if manifest.TotalBytes != info.Size() || currentInfo.Size() != info.Size() || !currentInfo.ModTime().Equal(info.ModTime()) {
		return api.FinalizeResult{}, ErrSourceChanged
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return api.FinalizeResult{}, err
	}
	if int64(len(body)) > limits.MaxManifestBytes || int64(len(manifest.Chunks)) > limits.MaxChunks {
		return api.FinalizeResult{}, fmt.Errorf("%q is larger than this server version accepts", name)
	}

	var upload api.Upload
	intent := api.CreateUploadRequest{ParentID: parent, FileID: manifest.FileID, VersionID: manifest.VersionID, Mode: protocol.Standard, ExpectedHead: "0",
		ChunkCount: int64(len(manifest.Chunks)), TransmittedBytes: manifest.TotalBytes, ManifestBytes: int64(len(body))}
	key := http.Header{api.IdempotencyKeyHeader: {string(randomID())}}
	if err := retryTransfer(ctx, func() error {
		return c.exchange(ctx, call{method: "POST", path: "/uploads", token: c.token(), header: key}, intent, &upload)
	}); err != nil {
		return api.FinalizeResult{}, err
	}
	if upload.ID.Validate() != nil {
		return api.FinalizeResult{}, errors.New("the server sent an invalid upload")
	}
	// A failed or cancelled upload releases its reservation now rather than at
	// expiry. A finalized upload refuses cancellation, so this is always safe.
	defer func() {
		if err != nil {
			release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = c.exchange(release, call{method: "DELETE", path: "/uploads/" + string(upload.ID), token: c.token()}, nil, nil)
		}
	}()
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return api.FinalizeResult{}, err
	}
	var sent int64
	if progress != nil {
		progress(0, manifest.TotalBytes)
	}
	for _, chunk := range manifest.Chunks {
		data := buffer[:chunk.Bytes]
		if _, err := io.ReadFull(file, data); err != nil {
			return api.FinalizeResult{}, ErrSourceChanged
		}
		if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != string(chunk.SHA256) {
			return api.FinalizeResult{}, ErrSourceChanged
		}
		header := http.Header{api.ChunkDigestHeader: {string(chunk.SHA256)}, "Content-Type": {"application/octet-stream"}}
		err := retryTransfer(ctx, func() error {
			response, e := c.do(ctx, call{method: "PUT", path: "/uploads/" + string(upload.ID) + "/chunks/" + strconv.FormatInt(chunk.Index, 10), token: c.token(), header: header, body: data})
			if e == nil {
				response.Body.Close()
			}
			return e
		})
		if err != nil {
			return api.FinalizeResult{}, err
		}
		sent += chunk.Bytes
		if progress != nil {
			progress(sent, manifest.TotalBytes)
		}
	}
	if n, _ := file.Read(buffer[:1]); n != 0 {
		return api.FinalizeResult{}, ErrSourceChanged
	}
	var result api.FinalizeResult
	err = retryTransfer(ctx, func() error {
		response, e := c.do(ctx, call{method: "POST", path: "/uploads/" + string(upload.ID) + "/finalize", token: c.token(), header: key, body: body, json: true})
		if e != nil {
			return e
		}
		defer response.Body.Close()
		result, e = api.DecodeFinalizeResult(response.Body)
		return e
	})
	if err != nil {
		return api.FinalizeResult{}, err
	}
	if result.FileID != manifest.FileID || result.VersionID != manifest.VersionID {
		return api.FinalizeResult{}, errors.New("the server sent an invalid finalization result")
	}
	if result.State != protocol.UploadCommitted {
		return result, api.Error{Code: api.Conflict, Message: "The file changed on the server during upload; the upload was kept as a separate version"}
	}
	return result, nil
}

// Manifest fetches and validates the manifest for a catalog entry's current
// version, binding it to the signed-in scope and the requested identifiers.
func (c *Client) Manifest(ctx context.Context, entry api.CatalogEntry) (protocol.Manifest, error) {
	account, signedIn := c.Account()
	if !signedIn {
		return protocol.Manifest{}, api.Error{Code: api.AuthenticationRequired, Message: "Sign in before downloading"}
	}
	limits, err := c.transferLimits(ctx)
	if err != nil {
		return protocol.Manifest{}, err
	}
	response, err := c.do(ctx, call{method: "GET", path: "/files/" + string(entry.ID) + "/versions/" + string(entry.HeadVersionID) + "/manifest", token: c.token()})
	if err != nil {
		return protocol.Manifest{}, err
	}
	defer response.Body.Close()
	manifest, err := api.DecodeManifest(response.Body, limits, nil)
	if err != nil {
		return protocol.Manifest{}, fmt.Errorf("reading file manifest: %w", err)
	}
	if manifest.FileID != entry.ID || manifest.VersionID != entry.HeadVersionID || manifest.Scope != (protocol.Scope{ServerID: account.ServerID, AccountID: account.AccountID}) {
		return protocol.Manifest{}, errors.New("the server sent a manifest that does not describe the requested file")
	}
	return manifest, nil
}

// Download restores one immutable version. Verified chunks from an interrupted
// attempt are reused, but always copied to a fresh exclusive temporary file:
// an existing partial (or a hard link at its path) is never opened for writing.
// Publication is an atomic rename in the selected destination directory.
func (c *Client) Download(ctx context.Context, entry api.CatalogEntry, destination string, progress Progress) (err error) {
	var manifest protocol.Manifest
	if err = retryTransfer(ctx, func() error {
		var e error
		manifest, e = c.Manifest(ctx, entry)
		return e
	}); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer root.Close()
	name := filepath.Base(destination)
	// Scope, version and destination bind partial data without putting remote
	// names or credentials into the checkpoint filename.
	identity := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%s/%s/%s", manifest.Scope.ServerID, manifest.Scope.AccountID, manifest.FileID, manifest.VersionID, name)))
	partial := fmt.Sprintf(".serversend-%x.partial", identity[:16])
	temporary := ".serversend-" + string(randomID()) + ".tmp"
	file, err := root.OpenFile(temporary, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { file.Close(); _ = root.Remove(temporary) }()
	// Read only ordinary checkpoint files. Refuse links even when contained
	// by Root; Root also prevents a replacement from escaping the directory.
	var previous *os.File
	if info, e := root.Lstat(partial); e == nil {
		if !info.Mode().IsRegular() {
			return errors.New("download checkpoint is not a regular file")
		}
		previous, err = root.Open(partial)
		if err != nil {
			return err
		}
		opened, e := previous.Stat()
		if e != nil || !os.SameFile(info, opened) {
			previous.Close()
			return errors.New("download checkpoint changed while opening")
		}
		defer previous.Close()
	} else if !os.IsNotExist(e) {
		return e
	}

	var chunkCapacity int64
	for _, chunk := range manifest.Chunks {
		chunkCapacity = max(chunkCapacity, chunk.Bytes)
	}
	buffer := make([]byte, chunkCapacity)
	var received int64
	var reusedChunks int
	// Revalidate and copy the contiguous prefix before replacing the old
	// checkpoint. Never mutate an existing checkpoint inode.
	if previous != nil {
		for _, chunk := range manifest.Chunks {
			if err = ctx.Err(); err != nil {
				return err
			}
			data := buffer[:chunk.Bytes]
			n, e := previous.ReadAt(data, received)
			sum := sha256.Sum256(data)
			if e != nil || int64(n) != chunk.Bytes || hex.EncodeToString(sum[:]) != string(chunk.SHA256) {
				break
			}
			if _, err = file.Write(data); err != nil {
				return err
			}
			received += chunk.Bytes
			reusedChunks++
		}
		previous.Close()
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = root.Rename(temporary, partial); err != nil {
		return err
	}
	// The stable name is installed before network work, so process exit leaves
	// discoverable data. Unverified tails are ignored on the next attempt.
	if progress != nil {
		progress(received, manifest.TotalBytes)
	}
	for _, chunk := range manifest.Chunks[reusedChunks:] {
		if err = ctx.Err(); err != nil {
			return err
		}
		data := buffer[:chunk.Bytes]
		err = retryTransfer(ctx, func() error {
			response, e := c.do(ctx, call{method: "GET", path: "/files/" + string(entry.ID) + "/versions/" + string(entry.HeadVersionID) + "/chunks/" + strconv.FormatInt(chunk.Index, 10), token: c.token()})
			if e != nil {
				return e
			}
			defer response.Body.Close()
			if _, e = io.ReadFull(response.Body, data); e != nil {
				return e
			}
			var extra [1]byte
			n, e := response.Body.Read(extra[:])
			if n != 0 {
				return errors.New("downloaded chunk exceeds its declared length")
			}
			if e != io.EOF {
				if e == nil {
					return io.ErrUnexpectedEOF
				}
				return e
			}
			return nil
		})
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != string(chunk.SHA256) {
			return errors.New("downloaded data failed verification; retry the download")
		}
		if _, err = file.Write(data); err != nil {
			return err
		}
		if err = file.Sync(); err != nil {
			return err
		}
		received += chunk.Bytes
		if progress != nil {
			progress(received, manifest.TotalBytes)
		}
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if previous != nil {
		previous.Close()
	}
	if err = root.Rename(partial, name); err != nil {
		return err
	}
	return nil
}
