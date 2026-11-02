package discord

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

type allowStore struct {
	mu   sync.Mutex
	path string

	Global  []string            `json:"global"`
	Session map[string][]string `json:"session"`
}

func newAllowStore(path string, log *slog.Logger) *allowStore {
	s := &allowStore{path: path, Session: map[string][]string{}}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Warn("allow store unreadable, starting with an empty allowlist", "path", path, "err", err)
	}
	if err == nil {
		if err := json.Unmarshal(data, s); err != nil {
			log.Warn("allow store corrupt, ignoring it", "path", path, "err", err)
			s.Global = nil
			s.Session = map[string][]string{}
		}
		if s.Session == nil {
			s.Session = map[string][]string{}
		}
	}
	return s
}

func (s *allowStore) persist(mutate func()) error {
	s.mu.Lock()
	mutate()
	data, err := json.MarshalIndent(s, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return writeAtomic(s.path, data)
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

func writeTemp(tmp *os.File, data []byte) error {
	if err := tmp.Chmod(0o600); err != nil {
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

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func remove(xs []string, x string) []string {
	out := xs[:0:0]
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}
