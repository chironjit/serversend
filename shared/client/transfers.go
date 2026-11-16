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

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
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

func (c *Client) UploadTo(ctx context.Context, path string, parent protocol.ID, progress Progress) (api.FinalizeResult, error) {
	account, signedIn := c.Account()
	if !signedIn {
		return api.FinalizeResult{}, api.Error{Code: api.AuthenticationRequired, Message: "Sign in before uploading"}
	}
	limits, err := c.transferLimits(ctx)
	if err != nil {
		return api.FinalizeResult{}, err
	}
	name := filepath.Base(path)
	if err := protocol.ValidateName(name); err != nil {
		return api.FinalizeResult{}, fmt.Errorf("%q cannot be used as a file name on the server", name)
	}
	file, err := os.Open(path)
	if err != nil {
		return api.FinalizeResult{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return api.FinalizeResult{}, err
	}
	if !info.Mode().IsRegular() {
		return api.FinalizeResult{}, fmt.Errorf("%q is not a regular file; folders are not supported yet", name)
	}
	chunkBytes := min(max(int64(preferredChunkBytes), limits.MinChunkBytes), limits.MaxChunkBytes)
	if info.Size()/chunkBytes >= limits.MaxChunks/2 || info.Size()/chunkBytes > limits.MaxManifestBytes/128 {
		chunkBytes = limits.MaxChunkBytes
	}
	buffer := make([]byte, chunkBytes)

	manifest := protocol.Manifest{SchemaVersion: protocol.ManifestVersion1, Scope: protocol.Scope{ServerID: account.ServerID, AccountID: account.AccountID},
		FileID: randomID(), VersionID: randomID(), Mode: protocol.Standard, Chunks: []protocol.Chunk{}, Standard: &protocol.StandardMetadata{Name: name}}
	for {
		n, err := io.ReadFull(file, buffer)
		if n > 0 {
			sum := sha256.Sum256(buffer[:n])
			manifest.Chunks = append(manifest.Chunks, protocol.Chunk{Index: int64(len(manifest.Chunks)), Bytes: int64(n), SHA256: protocol.Digest(hex.EncodeToString(sum[:]))})
			manifest.TotalBytes += int64(n)
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
	if err := c.exchange(ctx, call{method: "POST", path: "/uploads", token: c.token(), header: key}, intent, &upload); err != nil {
		return api.FinalizeResult{}, err
	}
	if upload.ID.Validate() != nil {
		return api.FinalizeResult{}, errors.New("the server sent an invalid upload")
	}
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
		response, err := c.do(ctx, call{method: "PUT", path: "/uploads/" + string(upload.ID) + "/chunks/" + strconv.FormatInt(chunk.Index, 10), token: c.token(), header: header, body: data})
		if err != nil {
			return api.FinalizeResult{}, err
		}
		response.Body.Close()
		sent += chunk.Bytes
		if progress != nil {
			progress(sent, manifest.TotalBytes)
		}
	}
	if n, _ := file.Read(buffer[:1]); n != 0 {
		return api.FinalizeResult{}, ErrSourceChanged
	}
	response, err := c.do(ctx, call{method: "POST", path: "/uploads/" + string(upload.ID) + "/finalize", token: c.token(), header: key, body: body, json: true})
	if err != nil {
		return api.FinalizeResult{}, err
	}
	defer response.Body.Close()
	result, err := api.DecodeFinalizeResult(response.Body)
	if err != nil || result.FileID != manifest.FileID || result.VersionID != manifest.VersionID {
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
	if err != nil || manifest.FileID != entry.ID || manifest.VersionID != entry.HeadVersionID || manifest.Scope != (protocol.Scope{ServerID: account.ServerID, AccountID: account.AccountID}) {
		return protocol.Manifest{}, errors.New("the server sent a manifest that does not describe the requested file")
	}
	return manifest, nil
}

// Download writes the entry's current version to destination. Every chunk is
// verified against the manifest before it is written; the destination appears
// only after the complete file has been flushed, replacing any existing file.
func (c *Client) Download(ctx context.Context, entry api.CatalogEntry, destination string, progress Progress) (err error) {
	manifest, err := c.Manifest(ctx, entry)
	if err != nil {
		return err
	}
	partial := destination + ".serversend-partial"
	file, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			file.Close()
			os.Remove(partial)
		}
	}()
	var received int64
	if progress != nil {
		progress(0, manifest.TotalBytes)
	}
	for _, chunk := range manifest.Chunks {
		response, err := c.do(ctx, call{method: "GET", path: "/files/" + string(entry.ID) + "/versions/" + string(entry.HeadVersionID) + "/chunks/" + strconv.FormatInt(chunk.Index, 10), token: c.token()})
		if err != nil {
			return err
		}
		hash := sha256.New()
		n, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, chunk.Bytes+1))
		response.Body.Close()
		if err != nil {
			return fmt.Errorf("download interrupted: %w", err)
		}
		if n != chunk.Bytes || hex.EncodeToString(hash.Sum(nil)) != string(chunk.SHA256) {
			return errors.New("downloaded data failed verification and was discarded; try again")
		}
		received += n
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
	return os.Rename(partial, destination)
}
