package main

import (
	"context"
	"errors"
	"github.com/chironjit/serversend/shared/api"
	"io"
	"os"

	"github.com/chironjit/serversend/apps/desktop/internal/transfer"
	"github.com/chironjit/serversend/shared/client"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// TransferEvent carries the latest state of recently changed transfers.
const TransferEvent = "transfer"

// TransferService is the frontend's only route to a server. It exposes
// display state and actions; credentials never cross into the webview.
type TransferService struct {
	manager *transfer.Manager
}

func NewTransferService(ctx context.Context) *TransferService {
	manager := transfer.New(ctx, func(items []transfer.Item) {
		if app := application.Get(); app != nil {
			app.Event.Emit(TransferEvent, items)
		}
	})
	manager.SetAppVersion(currentVersion)
	return &TransferService{manager: manager}
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

// CancelTransfer stops one queued or active transfer.
func (s *TransferService) CancelTransfer(id int) { s.manager.CancelTransfer(id) }

// CancelBatch stops an upload selection: scanning ends, queued files are
// skipped and active ones interrupted. Files already uploaded remain.
func (s *TransferService) CancelBatch(id int) { s.manager.CancelBatch(id) }

// Batches returns bounded summaries of recent upload selections, including skipped paths.
func (s *TransferService) Batches() []transfer.Batch { return s.manager.Batches() }

// ChooseAndUpload opens the native file picker and queues the selection. It
// returns how many paths were selected; links and unsupported files are reported
// in Batches rather than uploaded. Cancelling the dialog queues none.
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

// ChooseAndUploadFolder returns as soon as scanning starts; see Batches.
func (s *TransferService) ChooseAndUploadFolder(parent string) error {
	path, err := application.Get().Dialog.OpenFile().SetTitle("Choose folder to upload").CanChooseFiles(false).CanChooseDirectories(true).PromptForSingleSelection()
	if err != nil || path == "" {
		return err
	}
	return s.manager.StartFolderUpload(path, parent)
}

func (s *TransferService) DeleteEntry(ctx context.Context, id string) error {
	return s.manager.DeleteEntry(ctx, id)
}

func (s *TransferService) Profiles() ([]transfer.Profile, error) { return s.manager.LoadProfiles() }
func (s *TransferService) OpenProfile(ctx context.Context, origin string) (transfer.Account, error) {
	return s.manager.OpenProfile(ctx, origin)
}
func (s *TransferService) RemoveProfile(origin string) error { return s.manager.RemoveProfile(origin) }
func (s *TransferService) SetRemember(remember bool) error   { return s.manager.SetRemember(remember) }

func (s *TransferService) SessionStatus(ctx context.Context) (transfer.Step, error) {
	return s.manager.SessionStatus(ctx)
}
func (s *TransferService) CompleteReauthentication(ctx context.Context, code string) (transfer.Account, error) {
	return s.manager.CompleteReauthentication(ctx, code)
}

func (s *TransferService) Devices(ctx context.Context, before string) ([]api.DeviceInfo, error) {
	return s.manager.Devices(ctx, before)
}
func (s *TransferService) RevokeDevice(ctx context.Context, id string) error {
	return s.manager.RevokeDevice(ctx, id)
}
func (s *TransferService) ChooseServerCA(ctx context.Context, address string) (client.ServerProbe, error) {
	path, err := application.Get().Dialog.OpenFile().SetTitle("Choose the public CA certificate from your administrator").CanChooseFiles(true).PromptForSingleSelection()
	if err != nil {
		return client.ServerProbe{}, err
	}
	if path == "" {
		return client.ServerProbe{}, errors.New("certificate selection cancelled")
	}
	file, err := os.Open(path)
	if err != nil {
		return client.ServerProbe{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 64<<10+1))
	if err != nil {
		return client.ServerProbe{}, err
	}
	return s.manager.ProbeAuthority(ctx, address, string(data))
}

func (s *TransferService) FilesPage(ctx context.Context, parent, cursor string) (transfer.FilePage, error) {
	return s.manager.FilesPage(ctx, parent, cursor)
}
func (s *TransferService) CatalogChanged(ctx context.Context, revision string) (bool, error) {
	return s.manager.CatalogChanged(ctx, revision)
}
func (s *TransferService) QueueAction(id int, batch bool, action string) error {
	return s.manager.QueueAction(id, batch, action)
}
func (s *TransferService) SetBandwidth(bytesPerSecond int64) error {
	return s.manager.SetBandwidth(bytesPerSecond)
}
func (s *TransferService) ChooseAndRestore(ids []string, policy string) (bool, error) {
	if len(ids) == 0 {
		return false, errors.New("select files or folders to restore")
	}
	path, err := application.Get().Dialog.OpenFile().SetTitle("Choose restore destination").CanChooseFiles(false).CanChooseDirectories(true).PromptForSingleSelection()
	if err != nil || path == "" {
		return false, err
	}
	return true, s.manager.Restore(ids, path, policy)
}

func (s *TransferService) Bandwidth() int64 { return s.manager.Bandwidth() }

func (s *TransferService) QueueHealth() string { return s.manager.QueueHealth() }

func (s *TransferService) StorageStatistics(ctx context.Context) (api.StorageStatistics, error) {
	return s.manager.StorageStatistics(ctx)
}

func (s *TransferService) History(ctx context.Context, id, cursor string) (api.VersionPage, error) {
	return s.manager.History(ctx, id, cursor)
}
func (s *TransferService) TrashPage(ctx context.Context, cursor string) (api.CatalogPage, error) {
	return s.manager.TrashPage(ctx, cursor)
}
func (s *TransferService) RestoreTrash(ctx context.Context, id string) error {
	return s.manager.RestoreTrash(ctx, id)
}
func (s *TransferService) PurgeTrash(ctx context.Context, id string) error {
	return s.manager.PurgeTrash(ctx, id)
}
func (s *TransferService) SelectVersion(ctx context.Context, id, version string) error {
	return s.manager.SelectVersion(ctx, id, version)
}
func (s *TransferService) ChooseAndDownloadVersion(id, version string) (bool, error) {
	name, ok := s.manager.FileName(id)
	if !ok {
		return false, errors.New("refresh the file list first")
	}
	destination, err := application.Get().Dialog.SaveFile().SetFilename(name).PromptForSingleSelection()
	if err != nil || destination == "" {
		return false, err
	}
	return true, s.manager.DownloadVersion(id, version, destination)
}
