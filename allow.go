package discord

import (
	"encoding/json"
	"os"
	"sync"
)

// allowStore is the gateway's own permission store, persisted as JSON beside the
// daemon state. It holds the global command allowlist (who may run slash
// commands at all) and a per-session allowlist (who may take part in a given
// session). It lives entirely in the plugin: the core never learns about it,
// keeping all Discord permission policy on the gateway side.
type allowStore struct {
	mu   sync.Mutex
	path string

	Global  []string            `json:"global"`
	Session map[string][]string `json:"session"`
}

func newAllowStore(path string) *allowStore {
	s := &allowStore{path: path, Session: map[string][]string{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, s)
		if s.Session == nil {
			s.Session = map[string][]string{}
		}
	}
	return s
}

func (s *allowStore) save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

// Allowed reports whether a user may run slash commands. An empty global list
// means "allow everyone" so the first operator can bootstrap by adding people.
func (s *allowStore) Allowed(user string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Global) == 0 {
		return true
	}
	return contains(s.Global, user)
}

func (s *allowStore) AddGlobal(user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !contains(s.Global, user) {
		s.Global = append(s.Global, user)
	}
	return s.save()
}

func (s *allowStore) RemoveGlobal(user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Global = remove(s.Global, user)
	return s.save()
}

func (s *allowStore) ListGlobal() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.Global...)
}

func (s *allowStore) AddSession(name, user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !contains(s.Session[name], user) {
		s.Session[name] = append(s.Session[name], user)
	}
	return s.save()
}

func (s *allowStore) RemoveSession(name, user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Session[name] = remove(s.Session[name], user)
	if len(s.Session[name]) == 0 {
		delete(s.Session, name)
	}
	return s.save()
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
