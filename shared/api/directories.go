package api

import (
	"errors"
	"io"

	"github.com/chironjit/serversend/shared/protocol"
)

type CreateDirectoryRequest struct {
	ID           protocol.ID                `json:"id"`
	ParentID     protocol.ID                `json:"parent_id,omitempty"`
	Mode         protocol.StorageMode       `json:"mode"`
	ExpectedHead protocol.Revision          `json:"expected_head"`
	Standard     *protocol.StandardMetadata `json:"standard,omitempty"`
	Private      *protocol.PrivateEnvelope  `json:"private,omitempty"`
}

func (r CreateDirectoryRequest) Validate(privateFormats []string) error {
	if r.ExpectedHead != "0" {
		return errors.New("directory creation requires an absent head")
	}
	return (CatalogEntry{ID: r.ID, ParentID: r.ParentID, Kind: Directory, Mode: r.Mode, HeadRevision: "1", Standard: r.Standard, Private: r.Private}).Validate(privateFormats)
}

func DecodeCreateDirectory(r io.Reader, privateFormats []string) (CreateDirectoryRequest, error) {
	var value CreateDirectoryRequest
	if err := decode(r, MaxControlBytes, &value); err != nil {
		return CreateDirectoryRequest{}, err
	}
	if err := value.Validate(privateFormats); err != nil {
		return CreateDirectoryRequest{}, err
	}
	return value, nil
}
