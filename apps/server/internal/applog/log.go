// Package applog provides bounded, persistent operator diagnostics.
package applog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxBytes = 2 << 20
const maxEntries = 5000

type Entry struct {
	ID      uint64    `json:"id"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}
type Store struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	size    int64
	next    uint64
	entries []Entry
	closed  bool
}

func Open(configDir string) (*Store, error) {
	dir := filepath.Join(configDir, "logs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "app.jsonl")}
	for _, path := range []string{s.path + ".1", s.path} {
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var e Entry
			if json.Unmarshal(scanner.Bytes(), &e) == nil {
				s.remember(e)
				if e.ID > s.next {
					s.next = e.ID
				}
			}
		}
		err = scanner.Err()
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	var err error
	s.file, err = os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	info, err := s.file.Stat()
	if err != nil {
		s.file.Close()
		return nil, err
	}
	s.size = info.Size()
	return s, nil
}
func (s *Store) remember(e Entry) {
	s.entries = append(s.entries, e)
	if len(s.entries) > maxEntries {
		copy(s.entries, s.entries[len(s.entries)-maxEntries:])
		s.entries = s.entries[:maxEntries]
	}
}
func (s *Store) Add(level, message string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if len(message) > 8192 {
		message = message[:8192]
	}
	s.next++
	e := Entry{s.next, time.Now().UTC(), level, message}
	s.remember(e)
	data, _ := json.Marshal(e)
	data = append(data, '\n')
	err := s.append(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ServerSend log persistence failed: %v\n", err)
	}
}
func (s *Store) append(data []byte) error {
	if s.file == nil {
		return fmt.Errorf("log file is closed")
	}
	if s.size+int64(len(data)) > maxBytes {
		if err := s.file.Close(); err != nil {
			return err
		}
		s.file = nil
		if err := os.Remove(s.path + ".1"); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(s.path, s.path+".1"); err != nil {
			return err
		}
		var err error
		s.file, err = os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		s.size = 0
	}
	n, err := s.file.Write(data)
	s.size += int64(n)
	return err
}
func (s *Store) Write(p []byte) (int, error) {
	s.Add("info", strings.TrimSpace(string(p)))
	return len(p), nil
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

// Page returns newest first; before is an exclusive stable cursor.
func (s *Store) Page(level, query string, before uint64) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []Entry{}
	query = strings.ToLower(query)
	for i := len(s.entries) - 1; i >= 0 && len(result) < 100; i-- {
		e := s.entries[i]
		if (before == 0 || e.ID < before) && (level == "" || e.Level == level) && strings.Contains(strings.ToLower(e.Message), query) {
			result = append(result, e)
		}
	}
	return result
}
