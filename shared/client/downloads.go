package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/chironjit/serversend/shared/vault"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/protocol"
	"github.com/chironjit/serversend/shared/source"
)

// Download restores one immutable version. Verified chunks from an interrupted
// attempt are reused, but always copied to a fresh exclusive temporary file:
// an existing partial (or a hard link at its path) is never opened for writing.
// Publication is an atomic rename in the selected destination directory.
func (c *Client) Download(ctx context.Context, entry api.CatalogEntry, destination string, progress Progress) error {
	return c.DownloadWithPolicy(ctx, entry, destination, "replace", progress)
}

var ErrDestinationExists = errors.New("restore destination already exists")

// DownloadWithPolicy restores an immutable file using a persisted collision
// choice: replace, skip or fail. Neither skip nor fail can overwrite a file
// that appears concurrently with publication.
func (c *Client) DownloadWithPolicy(ctx context.Context, entry api.CatalogEntry, destination, policy string, progress Progress) (err error) {
	if policy != "replace" && policy != "skip" && policy != "fail" {
		return errors.New("choose replace, skip or fail for existing files")
	}
	if err := ValidateRestorePath(destination); err != nil {
		return err
	}
	unlock, err := lockDownloadDestination(ctx, destination)
	if err != nil {
		return err
	}
	defer unlock()
	root, err := openRestoreRoot(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer root.Close()
	name := filepath.Base(destination)
	if err = checkRestoreDestination(root, name, destination, policy); err != nil {
		return err
	}

	var manifest protocol.Manifest
	if err = retryTransfer(ctx, func() error {
		var e error
		manifest, e = c.Manifest(ctx, entry)
		return e
	}); err != nil {
		return err
	}
	var privateKey vault.Key
	var privateMeta vault.Metadata
	if manifest.Mode == protocol.Private {
		master, e := c.VaultKey()
		if e != nil {
			return e
		}
		privateMeta, privateKey, err = vault.ReadMetadata(master, manifest)
		clear(master[:])
		if err != nil {
			return err
		}
		defer clear(privateKey[:])
	}
	// Scope, version and destination bind partial data without putting remote
	// names or credentials into the checkpoint filename.
	partial := downloadPartialName(manifest.Scope, entry, name)
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
	if err = syncRestoreDirectory(root); err != nil {
		return err
	}
	// The stable name is installed before network work, so process exit leaves
	// discoverable data. Unverified tails are ignored on the next attempt.
	if progress != nil {
		progress(received, manifest.TotalBytes)
	}
	mismatched := false
	for _, chunk := range manifest.Chunks[reusedChunks:] {
		if err = ctx.Err(); err != nil {
			return err
		}
		data, err := c.downloadChunk(ctx, entry, chunk, buffer)
		if err != nil {
			return err
		}
		if !chunkMatches(data, chunk) {
			// Fetch once more to rule out a transient read error, then keep
			// the server's current bytes and report the mismatch.
			if data, err = c.downloadChunk(ctx, entry, chunk, buffer); err != nil {
				return err
			}
			if !chunkMatches(data, chunk) {
				if manifest.Mode == protocol.Private {
					return errors.New("the server copy of this private file is damaged and cannot be decrypted")
				}
				mismatched = true
			}
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
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = checkRestoreDestination(root, name, destination, policy); err != nil {
		return err
	}
	publish := partial
	if manifest.Mode == protocol.Private {
		encrypted, e := root.Open(partial)
		if e != nil {
			return e
		}
		defer encrypted.Close()
		publish = ".serversend-" + string(randomID()) + ".tmp"
		plain, e := root.OpenFile(publish, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		defer plain.Close()
		defer root.Remove(publish)
		hash := sha256.New()
		var total int64
		for _, chunk := range manifest.Chunks {
			if e = ctx.Err(); e != nil {
				return e
			}
			if _, e = c.VaultKey(); e != nil {
				return e
			}
			data := make([]byte, chunk.Bytes)
			if _, e = io.ReadFull(encrypted, data); e != nil {
				return e
			}
			b := vault.Binding{Scope: manifest.Scope, FileID: manifest.FileID, VersionID: manifest.VersionID, Kind: "chunk", Index: chunk.Index}
			decoded, e := vault.Open(privateKey, data, b.Bytes())
			if e != nil {
				return e
			}
			total += int64(len(decoded))
			hash.Write(decoded)
			_, e = plain.Write(decoded)
			clear(decoded)
			if e != nil {
				return e
			}
		}
		if total != privateMeta.Bytes || hex.EncodeToString(hash.Sum(nil)) != string(privateMeta.SHA256) {
			return errors.New("private plaintext length mismatch")
		}
		if e = plain.Sync(); e != nil {
			return e
		}
		if e = plain.Close(); e != nil {
			return e
		}
	}
	if policy == "replace" {
		if err = root.Rename(publish, name); err != nil {
			return err
		}
	} else {
		if err = root.Link(publish, name); err != nil {
			if os.IsExist(err) {
				if policy == "skip" {
					return &source.SkipError{Path: destination, Reason: "Existing file skipped"}
				}
				return ErrDestinationExists
			}
			return err
		}
		if err = root.Remove(partial); err != nil {
			return err
		}
	}
	if manifest.Mode == protocol.Private {
		_ = root.Remove(partial)
	}
	if err = syncRestoreDirectory(root); err != nil {
		return err
	}
	if mismatched {
		return ErrContentMismatch
	}
	return nil
}

// ErrContentMismatch is returned after a standard file was restored from a
// server copy that no longer matches the checksums recorded at upload, for
// example after it was edited or damaged on the server's storage. The
// destination holds the server's current bytes.
var ErrContentMismatch = errors.New("restored, but the server copy no longer matches the uploaded file")

func chunkMatches(data []byte, chunk protocol.Chunk) bool {
	sum := sha256.Sum256(data)
	return int64(len(data)) == chunk.Bytes && hex.EncodeToString(sum[:]) == string(chunk.SHA256)
}

// downloadChunk reads one chunk into buffer. A server copy that changed size
// declares a shorter length up front; anything else must be complete.
func (c *Client) downloadChunk(ctx context.Context, entry api.CatalogEntry, chunk protocol.Chunk, buffer []byte) (data []byte, err error) {
	err = retryTransfer(ctx, func() error {
		response, e := c.do(ctx, call{method: "GET", path: "/files/" + string(entry.ID) + "/versions/" + string(entry.HeadVersionID) + "/chunks/" + strconv.FormatInt(chunk.Index, 10), token: c.token()})
		if e != nil {
			return e
		}
		defer response.Body.Close()
		data = buffer[:chunk.Bytes]
		n, e := io.ReadFull(response.Body, data)
		if e != nil {
			if (e == io.EOF || e == io.ErrUnexpectedEOF) && response.ContentLength == int64(n) {
				data = data[:n] // complete, as declared, but shorter than recorded
				return nil
			}
			return e
		}
		var extra [1]byte
		n, e = response.Body.Read(extra[:])
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
	return data, err
}

func checkRestoreDestination(root *os.Root, name, destination, policy string) error {
	if err := rejectRestoreAlias(root, name); err != nil {
		return err
	}
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("restore destination is a link, directory or special file")
	}
	switch policy {
	case "skip":
		return &source.SkipError{Path: destination, Reason: "Existing file skipped"}
	case "fail":
		return ErrDestinationExists
	}
	return nil
}

// DownloadMatches reconciles an interrupted publication acknowledgement. It
// reads without following source links and verifies the destination against the
// selected immutable manifest; it never changes an existing local file.
func (c *Client) DownloadMatches(ctx context.Context, entry api.CatalogEntry, destination string) (bool, error) {
	if err := ValidateRestorePath(destination); err != nil {
		return false, err
	}
	file, err := source.Open(destination)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	var manifest protocol.Manifest
	if err = retryTransfer(ctx, func() error { var e error; manifest, e = c.Manifest(ctx, entry); return e }); err != nil {
		return false, err
	}
	if manifest.Mode == protocol.Private {
		master, err := c.VaultKey()
		if err != nil {
			return false, err
		}
		defer clear(master[:])
		meta, k, err := vault.ReadMetadata(master, manifest)
		clear(k[:])
		if err != nil {
			return false, err
		}
		if info.Size() != meta.Bytes {
			return false, nil
		}
		hash := sha256.New()
		buffer := make([]byte, 1<<20)
		var size int64
		for {
			if err = ctx.Err(); err != nil {
				return false, err
			}
			if _, err = c.VaultKey(); err != nil {
				return false, err
			}
			n, e := file.Read(buffer)
			if n > 0 {
				size += int64(n)
				hash.Write(buffer[:n])
			}
			if e == io.EOF {
				break
			}
			if e != nil {
				return false, e
			}
		}
		clear(buffer)
		return size == meta.Bytes && hex.EncodeToString(hash.Sum(nil)) == string(meta.SHA256), nil
	}
	if info.Size() != manifest.TotalBytes {
		return false, nil
	}
	var capacity int64
	for _, chunk := range manifest.Chunks {
		capacity = max(capacity, chunk.Bytes)
	}
	buffer := make([]byte, capacity)
	for _, chunk := range manifest.Chunks {
		if err = ctx.Err(); err != nil {
			return false, err
		}
		data := buffer[:chunk.Bytes]
		if _, err = io.ReadFull(file, data); err != nil {
			return false, err
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != string(chunk.SHA256) {
			return false, nil
		}
	}
	final, err := file.Stat()
	if err != nil {
		return false, err
	}
	return final.Size() == info.Size() && final.ModTime().Equal(info.ModTime()), nil
}

// CancelDownload removes only this account/version/destination's local partial
// file. An already published destination is never removed or rolled back.
func (c *Client) CancelDownload(ctx context.Context, entry api.CatalogEntry, destination string) error {
	if err := ValidateRestorePath(destination); err != nil {
		return err
	}
	account, ok := c.Account()
	if !ok {
		return api.Error{Code: api.AuthenticationRequired, Message: "Sign in before cancelling this restore"}
	}
	if entry.ID.Validate() != nil || entry.HeadVersionID.Validate() != nil {
		return errors.New("invalid restore version")
	}
	unlock, err := lockDownloadDestination(ctx, destination)
	if err != nil {
		return err
	}
	defer unlock()
	root, err := openRestoreRoot(filepath.Dir(destination))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	partial := downloadPartialName(protocol.Scope{ServerID: account.ServerID, AccountID: account.AccountID}, entry, filepath.Base(destination))
	info, err := root.Lstat(partial)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("download checkpoint is not a regular file")
	}
	if err := root.Remove(partial); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncRestoreDirectory(root)
}

func downloadPartialName(scope protocol.Scope, entry api.CatalogEntry, name string) string {
	identity := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%s/%s/%s", scope.ServerID, scope.AccountID, entry.ID, entry.HeadVersionID, name)))
	return fmt.Sprintf(".serversend-%x.partial", identity[:16])
}
