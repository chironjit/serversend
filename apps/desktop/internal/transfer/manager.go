// Package transfer manages trusted desktop connections and foreground transfers.
package transfer

import (
	"context"
	"encoding/base64"
	"errors"
	"github.com/chironjit/serversend/shared/protocol"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
	"github.com/chironjit/serversend/shared/source"
)

// Step tells the frontend which screen follows; it carries no credential.
type Step struct {
	Next string `json:"next"`
}

type Enrollment struct {
	Secret string `json:"secret"`
	QR     string `json:"qr"` // data: URL of the provisioning QR code
}

type Account struct {
	ReauthenticationRequired bool   `json:"reauthenticationRequired"`
	Username                 string `json:"username"`
	Origin                   string `json:"origin"`
	ServerID                 string `json:"serverId"`
}

type File struct {
	Directory bool   `json:"directory"`
	ID        string `json:"id"`
	Name      string `json:"name"`
}

// Item is one upload or download as shown in the transfer list.
type Item struct {
	BatchID   int     `json:"batchId"`
	Speed     float64 `json:"speed"`
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	Direction string  `json:"direction"` // "upload" or "download"
	State     string  `json:"state"`     // "queued", "active", "done", "skipped", "cancelled" or "failed"
	Done      int64   `json:"done"`
	Total     int64   `json:"total"`
	Error     string  `json:"error"`
}

type job struct {
	ctx    context.Context // cancelled by CancelTransfer or CancelBatch
	size   int64           // at queue time; batching uses it as a hint
	item   Item
	path   string
	entry  api.CatalogEntry
	parent protocol.ID
	client *client.Client
}

type Manager struct {
	ctx          context.Context
	connectionMu sync.Mutex
	version      string
	emit         func(Item)

	mu          sync.Mutex
	authority   string
	probe       client.ServerProbe
	client      *client.Client
	username    string
	password    string
	challenge   string
	catalog     map[string]api.CatalogEntry
	items       []Item
	cancels     map[int]context.CancelFunc
	queue       chan job
	batches     map[int]*Batch
	nextBatch   int
	profilePath string
	profiles    []Profile
}

// workers transfer files concurrently. Small files are dominated by request
// round trips, which overlap; memory stays bounded to one chunk buffer each.
const workers = 8

// New starts the transfer workers. emit receives the latest state of changed
// transfers at most five times a second, from its own goroutine, so a slow
// window can neither stall transfers nor be flooded by many small files.
func New(ctx context.Context, emit func([]Item)) *Manager {
	if emit == nil {
		emit = func([]Item) {}
	}
	m := &Manager{ctx: ctx, emit: coalesce(ctx, emit), queue: make(chan job, 4096), catalog: map[string]api.CatalogEntry{}}
	for range workers {
		go m.work(ctx)
	}
	return m
}

// coalesce keeps only the newest state per transfer between deliveries.
func coalesce(ctx context.Context, emit func([]Item)) func(Item) {
	var mu sync.Mutex
	changed := map[int]Item{}
	wake := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-wake:
			}
			mu.Lock()
			batch := make([]Item, 0, len(changed))
			for _, item := range changed {
				batch = append(batch, item)
			}
			changed = map[int]Item{}
			mu.Unlock()
			sort.Slice(batch, func(i, j int) bool { return batch[i].ID < batch[j].ID })
			emit(batch)
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}()
	return func(item Item) {
		mu.Lock()
		changed[item.ID] = item
		mu.Unlock()
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// message converts any failure into text that is safe and useful to display.
func message(err error) error {
	var typed api.Error
	if errors.As(err, &typed) {
		return errors.New(typed.Message)
	}
	return err
}

// Probe inspects the leaf without sending credentials. Private deployments
// must additionally import and compare the operator-supplied public CA.
func (m *Manager) Probe(ctx context.Context, origin string) (client.ServerProbe, error) {
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	if !strings.Contains(origin, "://") {
		origin = "https://" + origin
	}
	u, err := url.Parse(origin)
	if err != nil {
		return client.ServerProbe{}, err
	}
	if u.Port() == "" {
		u.Host = net.JoinHostPort(u.Hostname(), "58443")
		origin = u.String()
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	probe, err := client.Probe(ctx, origin)
	if err != nil {
		return client.ServerProbe{}, err
	}
	m.mu.Lock()
	m.probe = probe
	m.authority = ""
	m.mu.Unlock()
	return probe, nil
}

// Approve verifies CA trust and the signed identity, then persists that pin.
// New connections try HTTP/3 first, with verified HTTP/2 fallback.
func (m *Manager) Approve(fingerprint string) error {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	if err := m.idle(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.probe.Fingerprint == "" || fingerprint != m.probe.Fingerprint {
		return errors.New("check the server again before approving it")
	}
	if !m.probe.PubliclyTrusted && m.authority == "" {
		return errors.New("import the server CA certificate supplied by your administrator before signing in")
	}
	c, err := client.NewVerified(m.probe.Origin, client.Trust{AuthorityPEM: m.authority})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = c.VerifyIdentity(ctx); err != nil {
		c.Close()
		return err
	}
	trust, err := c.VerifiedTrust()
	if err != nil {
		c.Close()
		return err
	}
	if err := m.saveTrustLocked(m.probe, trust); err != nil {
		c.Close()
		return err
	}
	if m.client != nil {
		m.client.Close()
	}
	m.client, m.challenge, m.password = c, "", ""
	c.SetAppVersion(m.version)
	m.configureStoreLocked(c)
	return nil
}

func (m *Manager) current() (*client.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client == nil {
		return nil, errors.New("connect to a server first")
	}
	return m.client, nil
}

// SignIn checks the password and reports the next required step. The password
// is kept in memory only until sign-in completes, to continue onboarding.
func (m *Manager) SignIn(ctx context.Context, username, password string) (Step, error) {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	if err := m.idle(); err != nil {
		return Step{}, err
	}
	c, err := m.current()
	if err != nil {
		return Step{}, err
	}
	attempt, err := c.Password(ctx, strings.TrimSpace(username), password)
	if err != nil {
		return Step{}, message(err)
	}
	m.mu.Lock()
	m.username, m.password, m.challenge = strings.TrimSpace(username), password, attempt.Challenge
	m.mu.Unlock()
	return Step{Next: attempt.NextStep}, nil
}

func (m *Manager) pending() (c *client.Client, username, password, challenge string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client == nil || m.challenge == "" {
		return nil, "", "", "", errors.New("your sign-in expired; enter your username and password again")
	}
	return m.client, m.username, m.password, m.challenge, nil
}

// ReplacePassword sets the permanent password and continues signing in with it.
func (m *Manager) ReplacePassword(ctx context.Context, password string) (Step, error) {
	c, username, _, challenge, err := m.pending()
	if err != nil {
		return Step{}, err
	}
	if err := c.ReplacePassword(ctx, challenge, password); err != nil {
		return Step{}, message(err)
	}
	return m.SignIn(ctx, username, password)
}

func (m *Manager) BeginEnrollment(ctx context.Context) (Enrollment, error) {
	c, _, _, challenge, err := m.pending()
	if err != nil {
		return Enrollment{}, err
	}
	enrollment, err := c.EnrollTOTP(ctx, challenge)
	if err != nil {
		return Enrollment{}, message(err)
	}
	return Enrollment{Secret: enrollment.Secret, QR: "data:image/png;base64," + base64.StdEncoding.EncodeToString(enrollment.QRPNG)}, nil
}

// ConfirmEnrollment returns the recovery codes once and prepares the final
// sign-in step. That step needs a new authenticator code or a recovery code,
// because the code used here has been consumed.
func (m *Manager) ConfirmEnrollment(ctx context.Context, code string) ([]string, error) {
	c, username, password, challenge, err := m.pending()
	if err != nil {
		return nil, err
	}
	codes, err := c.ConfirmTOTP(ctx, challenge, strings.TrimSpace(code))
	if err != nil {
		return nil, message(err)
	}
	if _, err := m.SignIn(ctx, username, password); err != nil {
		// Enrollment succeeded; the user can still sign in from the start.
		m.mu.Lock()
		m.challenge = ""
		m.mu.Unlock()
	}
	return codes, nil
}

// CompleteSignIn accepts an authenticator code or a recovery code.
func (m *Manager) CompleteSignIn(ctx context.Context, code string) (Account, error) {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	c, _, _, challenge, err := m.pending()
	if err != nil {
		return Account{}, err
	}
	device, _ := os.Hostname()
	if api.ValidateDeviceName(device) != nil {
		device = "Desktop"
	}
	session, err := c.SignIn(ctx, challenge, strings.TrimSpace(code), device)
	if err != nil {
		return Account{}, message(err)
	}
	if session.Role == "admin" {
		_ = c.SignOut(ctx)
		return Account{}, errors.New("Use the web admin interface at " + c.Origin() + "/admin/; sign in here with a user account")
	}
	m.mu.Lock()
	m.password, m.challenge = "", ""
	m.mu.Unlock()
	if err := m.saveLogin(c); err != nil {
		return Account{}, err
	}
	return Account{Username: session.Username, Origin: c.Origin(), ServerID: string(session.ServerID)}, nil
}

func (m *Manager) SignOut(ctx context.Context) error {
	m.connectionMu.Lock()
	defer m.connectionMu.Unlock()
	if err := m.idle(); err != nil {
		return err
	}
	m.mu.Lock()
	c := m.client
	m.password, m.challenge, m.catalog = "", "", map[string]api.CatalogEntry{}
	m.mu.Unlock()
	if c == nil {
		return nil
	}
	credentialErr := m.forgetLogin(c.Origin())
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return errors.Join(credentialErr, message(c.SignOut(ctx)))
}

// Files lists the account's files sorted by name.
func (m *Manager) Files(ctx context.Context, parent string) ([]File, error) {
	c, err := m.current()
	if err != nil {
		return nil, err
	}
	page, err := c.CatalogFolder(ctx, protocol.ID(parent))
	if err != nil {
		return nil, message(err)
	}
	files := make([]File, 0, len(page.Entries))
	catalog := make(map[string]api.CatalogEntry, len(page.Entries))
	for _, entry := range page.Entries {
		if entry.Standard == nil || entry.Trashed {
			continue
		}
		catalog[string(entry.ID)] = entry
		files = append(files, File{ID: string(entry.ID), Name: entry.Standard.Name, Directory: entry.Kind == api.Directory})
	}
	m.mu.Lock()
	m.catalog = catalog
	m.mu.Unlock()
	sortFiles(files)
	return files, nil
}

// FileName returns the listed name for a download dialog's default.
func (m *Manager) FileName(id string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.catalog[id]
	if !ok || entry.Kind != api.File {
		return "", false
	}
	return entry.Standard.Name, true
}

// register records a queued transfer with its own cancellable context.
func (m *Manager) register(parent context.Context, j job) job {
	var cancel context.CancelFunc
	j.ctx, cancel = context.WithCancel(parent)
	m.mu.Lock()
	j.item.ID, j.item.State = len(m.items)+1, "queued"
	m.items = append(m.items, j.item)
	if m.cancels == nil {
		m.cancels = map[int]context.CancelFunc{}
	}
	m.cancels[j.item.ID] = cancel
	m.queuedLocked(j.item.BatchID)
	m.mu.Unlock()
	m.emit(j.item)
	return j
}

func (m *Manager) enqueue(parent context.Context, j job) error {
	j = m.register(parent, j)
	select {
	case m.queue <- j:
		return nil
	default:
		err := errors.New("too many transfers are waiting; try again when some finish")
		m.finish(j, err)
		return err
	}
}

// CancelTransfer stops one queued or active transfer.
func (m *Manager) CancelTransfer(id int) {
	m.mu.Lock()
	cancel := m.cancels[id]
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (m *Manager) base() context.Context {
	if m.ctx == nil {
		return context.Background()
	}
	return m.ctx
}

// Upload queues local files. Each is published as a new file on the server.
func (m *Manager) Upload(paths []string, parent string) (err error) {
	c, err := m.current()
	if err != nil {
		return err
	}
	batchID, ctx := m.startBatch(m.base(), "Selected files")
	defer func() { m.endScan(batchID, err) }()
	for _, path := range paths {
		f, openErr := source.Open(path)
		if skip, ok := source.Skipped(openErr); ok {
			m.skip(batchID, skip)
			continue
		}
		if openErr != nil {
			return openErr
		}
		info, statErr := f.Stat()
		f.Close()
		if statErr != nil {
			return statErr
		}
		if info.IsDir() {
			m.skip(batchID, &source.SkipError{Path: path, Reason: "Directory skipped; use Upload folder to preserve its contents"})
			continue
		}
		if err := m.enqueue(ctx, job{client: c, parent: protocol.ID(parent), path: path, size: info.Size(), item: Item{BatchID: batchID, Name: filepath.Base(path), Direction: "upload", Total: info.Size()}}); err != nil {
			return err
		}
	}
	return nil
}

// Download queues a listed file for restoring to destination.
func (m *Manager) Download(id, destination string) error {
	m.mu.Lock()
	entry, ok := m.catalog[id]
	m.mu.Unlock()
	if !ok {
		return errors.New("refresh the file list and choose the file again")
	}
	c, err := m.current()
	if err != nil {
		return err
	}
	return m.enqueue(m.base(), job{client: c, path: destination, entry: entry, item: Item{Name: entry.Standard.Name, Direction: "download"}})
}

// Transfers returns every transfer of this run, oldest first.
func (m *Manager) Transfers() []Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Item{}, m.items...)
}

func (m *Manager) update(id int, change func(*Item)) {
	m.mu.Lock()
	item := &m.items[id-1]
	change(item)
	snapshot := *item
	m.mu.Unlock()
	m.emit(snapshot)
}

func sortFiles(files []File) {
	sort.Slice(files, func(i, j int) bool {
		if a, b := strings.ToLower(files[i].Name), strings.ToLower(files[j].Name); a != b {
			return a < b
		}
		return files[i].ID < files[j].ID
	})
}

func (m *Manager) idle() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, batch := range m.batches {
		if batch.scanning || batch.pending > 0 {
			return errors.New("wait for transfers to finish before changing accounts or servers")
		}
	}
	for _, item := range m.items {
		if item.State == "queued" || item.State == "active" {
			return errors.New("wait for transfers to finish before changing accounts or servers")
		}
	}
	return nil
}

func (m *Manager) SetAppVersion(version string) { m.version = version }
