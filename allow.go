package discord

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

var errStoreLocked = errors.New("the state file cannot be read, so it is kept untouched and changes are refused")

type allowStore struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	path    string
	locked  error
	write   func(path string, data []byte) error

	Global  []string            `json:"global"`
	Session map[string][]string `json:"session"`
}

func newAllowStore(path string, log *slog.Logger) *allowStore {
	store := &allowStore{path: path, write: writeAtomic}
	if err := loadStoreFile(path, store); err != nil {
		store.Global, store.Session = nil, nil
		store.locked = fmt.Errorf("%w: %w", errStoreLocked, err)
		log.Warn("allow store unreadable or corrupt, access is refused until the file is repaired", "path", path, "err", err)
	}
	if store.Session == nil {
		store.Session = make(map[string][]string)
	}
	return store
}

func loadStoreFile(path string, dst any) error {
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dst)
}

func (s *allowStore) persist(mutate func()) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	if s.locked != nil {
		s.mu.Unlock()
		return s.locked
	}
	mutate()
	data, err := json.MarshalIndent(s, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.write(s.path, data)
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	if err := writeTemp(tmp, data); err != nil {
		return errors.Join(err, removeTemp(tmp.Name()))
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return errors.Join(err, removeTemp(tmp.Name()))
	}
	return nil
}

const privateFileMode = 0o600

func writeTemp(tmp *os.File, data []byte) error {
	if err := tmp.Chmod(privateFileMode); err != nil {
		return errors.Join(err, tmp.Close())
	}
	if _, err := tmp.Write(data); err != nil {
		return errors.Join(err, tmp.Close())
	}
	return tmp.Close()
}

func removeTemp(name string) error {
	if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (s *allowStore) Allowed(user string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locked != nil {
		return false
	}
	if len(s.Global) == 0 {
		return true
	}
	return contains(s.Global, user)
}

func (s *allowStore) AddGlobal(user string) error {
	return s.persist(func() {
		if !contains(s.Global, user) {
			s.Global = append(s.Global, user)
		}
	})
}

func (s *allowStore) RemoveGlobal(user string) error {
	return s.persist(func() {
		s.Global = remove(s.Global, user)
	})
}

func (s *allowStore) ListGlobal() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.Global...)
}

func (s *allowStore) AddSession(name, user string) error {
	return s.persist(func() {
		if !contains(s.Session[name], user) {
			s.Session[name] = append(s.Session[name], user)
		}
	})
}

func (s *allowStore) RemoveSession(name, user string) error {
	return s.persist(func() {
		s.Session[name] = remove(s.Session[name], user)
		if len(s.Session[name]) == 0 {
			delete(s.Session, name)
		}
	})
}

func (s *allowStore) ListSession(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.Session[name]...)
}

func contains(xs []string, item string) bool {
	for _, v := range xs {
		if v == item {
			return true
		}
	}
	return false
}

func remove(items []string, item string) []string {
	out := items[:0:0]
	for _, value := range items {
		if value != item {
			out = append(out, value)
		}
	}
	return out
}
