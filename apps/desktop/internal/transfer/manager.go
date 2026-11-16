// Package transfer holds the desktop's connection to one server for the
// vertical slice: approve a server, sign in, upload, list and download.
// Passwords, challenges and the access token stay in this Go process; the
// frontend receives only display state. Nothing here is persisted yet, so the
// user signs in again after restarting the application.
package transfer

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chironjit/serversend/shared/api"
	"github.com/chironjit/serversend/shared/client"
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
	Username string `json:"username"`
	Origin   string `json:"origin"`
	ServerID string `json:"serverId"`
}

type File struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Item is one upload or download as shown in the transfer list.
type Item struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Direction string `json:"direction"` // "upload" or "download"
	State     string `json:"state"`     // "queued", "active", "done" or "failed"
	Done      int64  `json:"done"`
	Total     int64  `json:"total"`
	Error     string `json:"error"`
}

type job struct {
	item  Item
	path  string
	entry api.CatalogEntry
}

type Manager struct {
	emit func(Item)

	mu        sync.Mutex
	probe     client.ServerProbe
	client    *client.Client
	username  string
	password  string
	challenge string
	catalog   map[string]api.CatalogEntry
	items     []Item
	queue     chan job
}

// New starts one worker, so transfers run in order and memory stays bounded
// to a single chunk buffer. emit receives every state change and must not block.
func New(ctx context.Context, emit func(Item)) *Manager {
	if emit == nil {
		emit = func(Item) {}
	}
	m := &Manager{emit: emit, queue: make(chan job, 4096), catalog: map[string]api.CatalogEntry{}}
	go m.work(ctx)
	return m
}

// message converts any failure into text that is safe and useful to display.
func message(err error) error {
	var typed api.Error
	if errors.As(err, &typed) {
		return errors.New(typed.Message)
	}
	return err
}

// Probe contacts a server without trusting it. The user compares the returned
// fingerprint with the operator's value before calling Approve.
func (m *Manager) Probe(ctx context.Context, origin string) (client.ServerProbe, error) {
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	if !strings.Contains(origin, "://") {
		origin = "https://" + origin
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	probe, err := client.Probe(ctx, origin)
	if err != nil {
		return client.ServerProbe{}, err
	}
	m.mu.Lock()
	m.probe = probe
	m.mu.Unlock()
	return probe, nil
}

// Approve pins exactly the certificate the last Probe displayed.
func (m *Manager) Approve(fingerprint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.probe.Fingerprint == "" || fingerprint != m.probe.Fingerprint {
		return errors.New("check the server again before approving it")
	}
	c, err := client.New(m.probe.Origin, fingerprint)
	if err != nil {
		return err
	}
	if m.client != nil {
		m.client.Close()
	}
	m.client, m.challenge, m.password = c, "", ""
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
	m.mu.Lock()
	m.password, m.challenge = "", ""
	m.mu.Unlock()
	return Account{Username: session.Username, Origin: c.Origin(), ServerID: string(session.ServerID)}, nil
}

func (m *Manager) SignOut(ctx context.Context) error {
	m.mu.Lock()
	c := m.client
	m.password, m.challenge, m.catalog = "", "", map[string]api.CatalogEntry{}
	m.mu.Unlock()
	if c == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return message(c.SignOut(ctx))
}

// Files lists the account's files sorted by name.
func (m *Manager) Files(ctx context.Context) ([]File, error) {
	c, err := m.current()
	if err != nil {
		return nil, err
	}
	page, err := c.Catalog(ctx)
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
		files = append(files, File{ID: string(entry.ID), Name: entry.Standard.Name})
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
	if !ok {
		return "", false
	}
	return entry.Standard.Name, true
}

func (m *Manager) enqueue(j job) error {
	m.mu.Lock()
	j.item.ID, j.item.State = len(m.items)+1, "queued"
	m.items = append(m.items, j.item)
	m.mu.Unlock()
	select {
	case m.queue <- j:
		m.emit(j.item)
		return nil
	default:
		m.update(j.item.ID, func(item *Item) {
			item.State, item.Error = "failed", "Too many transfers are waiting; try again when some finish"
		})
		return errors.New("too many transfers are waiting")
	}
}

// Upload queues local files. Each is published as a new file on the server.
func (m *Manager) Upload(paths []string) error {
	if _, err := m.current(); err != nil {
		return err
	}
	for _, path := range paths {
		if err := m.enqueue(job{path: path, item: Item{Name: filepath.Base(path), Direction: "upload"}}); err != nil {
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
	return m.enqueue(job{path: destination, entry: entry, item: Item{Name: entry.Standard.Name, Direction: "download"}})
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

func (m *Manager) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-m.queue:
			m.run(ctx, j)
		}
	}
}

func (m *Manager) run(ctx context.Context, j job) {
	id := j.item.ID
	c, err := m.current()
	if err == nil {
		m.update(id, func(item *Item) { item.State = "active" })
		// Progress is reported at most ten times a second so a slow window
		// cannot hold back the transfer or flood the frontend.
		var last time.Time
		progress := func(done, total int64) {
			if now := time.Now(); done == total || now.Sub(last) >= 100*time.Millisecond {
				last = now
				m.update(id, func(item *Item) { item.Done, item.Total = done, total })
			}
		}
		if j.item.Direction == "upload" {
			_, err = c.Upload(ctx, j.path, progress)
		} else {
			err = c.Download(ctx, j.entry, j.path, progress)
		}
	}
	m.update(id, func(item *Item) {
		item.State = "done"
		if err != nil {
			item.State, item.Error = "failed", message(err).Error()
		}
	})
}

func sortFiles(files []File) {
	sort.Slice(files, func(i, j int) bool {
		if a, b := strings.ToLower(files[i].Name), strings.ToLower(files[j].Name); a != b {
			return a < b
		}
		return files[i].ID < files[j].ID
	})
}
