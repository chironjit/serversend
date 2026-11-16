package main

import (
	"context"
	"errors"

	"github.com/chironjit/serversend/apps/desktop/internal/transfer"
	"github.com/chironjit/serversend/shared/client"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// TransferEvent is emitted with a transfer.Item whenever a transfer changes.
const TransferEvent = "transfer"

// TransferService is the frontend's only route to a server. It exposes
// display state and actions; credentials never cross into the webview.
type TransferService struct {
	manager *transfer.Manager
}

func NewTransferService(ctx context.Context) *TransferService {
	return &TransferService{manager: transfer.New(ctx, func(item transfer.Item) {
		if app := application.Get(); app != nil {
			app.Event.Emit(TransferEvent, item)
		}
	})}
}

func (s *TransferService) Probe(ctx context.Context, address string) (client.ServerProbe, error) {
	return s.manager.Probe(ctx, address)
}

func (s *TransferService) Approve(fingerprint string) error { return s.manager.Approve(fingerprint) }

func (s *TransferService) SignIn(ctx context.Context, username, password string) (transfer.Step, error) {
	return s.manager.SignIn(ctx, username, password)
}

func (s *TransferService) ReplacePassword(ctx context.Context, password string) (transfer.Step, error) {
	return s.manager.ReplacePassword(ctx, password)
}

func (s *TransferService) BeginEnrollment(ctx context.Context) (transfer.Enrollment, error) {
	return s.manager.BeginEnrollment(ctx)
}

func (s *TransferService) ConfirmEnrollment(ctx context.Context, code string) ([]string, error) {
	return s.manager.ConfirmEnrollment(ctx, code)
}

func (s *TransferService) CompleteSignIn(ctx context.Context, code string) (transfer.Account, error) {
	return s.manager.CompleteSignIn(ctx, code)
}

func (s *TransferService) SignOut(ctx context.Context) error { return s.manager.SignOut(ctx) }

func (s *TransferService) Files(ctx context.Context, parent string) ([]transfer.File, error) {
	return s.manager.Files(ctx, parent)
}

func (s *TransferService) Transfers() []transfer.Item { return s.manager.Transfers() }

// ChooseAndUpload opens the native file picker and queues the selection. It
// returns how many files were queued; cancelling the dialog queues none.
func (s *TransferService) ChooseAndUpload(parent string) (int, error) {
	paths, err := application.Get().Dialog.OpenFile().SetTitle("Choose files to upload").CanChooseFiles(true).PromptForMultipleSelection()
	if err != nil || len(paths) == 0 {
		return 0, err
	}
	return len(paths), s.manager.Upload(paths, parent)
}

// ChooseAndDownload asks where to save a listed file and queues the download.
// It reports false when the user cancels the dialog.
func (s *TransferService) ChooseAndDownload(fileID string) (bool, error) {
	name, ok := s.manager.FileName(fileID)
	if !ok {
		return false, errors.New("refresh the file list and choose the file again")
	}
	destination, err := application.Get().Dialog.SaveFile().SetFilename(name).PromptForSingleSelection()
	if err != nil || destination == "" {
		return false, err
	}
	return true, s.manager.Download(fileID, destination)
}

func (s *TransferService) CreateDirectory(ctx context.Context, parent, name string) error {
	return s.manager.CreateDirectory(ctx, parent, name)
}
func (s *TransferService) ChooseAndUploadFolder(ctx context.Context, parent string) error {
	path, err := application.Get().Dialog.OpenFile().SetTitle("Choose folder to upload").CanChooseFiles(false).CanChooseDirectories(true).PromptForSingleSelection()
	if err != nil || path == "" {
		return err
	}
	return s.manager.UploadFolder(ctx, path, parent)
}
