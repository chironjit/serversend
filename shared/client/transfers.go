package client

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

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

func (c *Client) UploadTo(ctx context.Context, path string, parent protocol.ID, progress Progress) (_ api.FinalizeResult, err error) {
	var checkpoint UploadCheckpoint
	defer func() {
		if err != nil && checkpoint.UploadID != "" {
			release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = c.CancelUpload(release, checkpoint.UploadID)
		}
	}()
	return c.UploadResumable(ctx, path, parent, &checkpoint, func(*UploadCheckpoint) error { return nil }, progress)
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
