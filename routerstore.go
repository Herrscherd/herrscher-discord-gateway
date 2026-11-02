package discord

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

type bindStore struct {
	mu   sync.Mutex
	path string

	Channels map[string]string `json:"channels"`
	Threads  map[string]bool   `json:"threads,omitempty"`
	Levels   map[string]string `json:"levels,omitempty"`
	Modes    map[string]string `json:"modes,omitempty"`
	Repos    map[string]string `json:"repos,omitempty"`
	Parents  map[string]string `json:"parents,omitempty"`
	Last     string            `json:"lastRepo,omitempty"`
}

func newBindStore(path string, log *slog.Logger) *bindStore {
	store := &bindStore{path: path, Channels: make(map[string]string), Threads: make(map[string]bool), Levels: make(map[string]string), Modes: make(map[string]string), Repos: make(map[string]string), Parents: make(map[string]string)}
	if data, err := os.ReadFile(filepath.Clean(path)); err == nil {
		if err := json.Unmarshal(data, store); err != nil {
			log.Warn("bind store corrupt, ignoring it", "path", path, "err", err)
			store.Channels, store.Threads, store.Levels = make(map[string]string), make(map[string]bool), make(map[string]string)
			store.Modes, store.Repos, store.Parents = make(map[string]string), make(map[string]string), make(map[string]string)
			store.Last = ""
		}
		if store.Channels == nil {
			store.Channels = make(map[string]string)
		}
		if store.Threads == nil {
			store.Threads = make(map[string]bool)
		}
		if store.Levels == nil {
			store.Levels = make(map[string]string)
		}
		if store.Modes == nil {
			store.Modes = make(map[string]string)
		}
		if store.Repos == nil {
			store.Repos = make(map[string]string)
		}
		if store.Parents == nil {
			store.Parents = make(map[string]string)
		}
	}
	return store
}

func (s *bindStore) persist(mutate func()) error {
	s.mu.Lock()
	mutate()
	data, err := json.MarshalIndent(s, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return writeAtomic(s.path, data)
}

func (s *bindStore) Session(channel string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Channels[channel]
}

func (s *bindStore) SessionsIn(conv string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for slot, session := range s.Channels {
		if convOfSlot(slot) == conv {
			out = append(out, session)
		}
	}
	sort.Strings(out)
	return out
}

func (s *bindStore) Parent(conv string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Parents[conv]
}

func (s *bindStore) Level(channel string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Levels[channel]
}

func (s *bindStore) SetLevel(channel, level string) error {
	return s.persist(func() {
		if level == "" {
			delete(s.Levels, channel)
			return
		}
		s.Levels[channel] = level
	})
}

func (s *bindStore) Mode(channel string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Modes[channel]
}

func (s *bindStore) SetMode(channel, mode string) error {
	return s.persist(func() {
		if mode == "" {
			delete(s.Modes, channel)
			return
		}
		s.Modes[channel] = mode
	})
}

func (s *bindStore) Repo(channel string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Repos[channel]
}

func (s *bindStore) SetRepo(channel, value string) error {
	return s.persist(func() {
		if value == "" {
			delete(s.Repos, channel)
			return
		}
		s.Repos[channel] = value
	})
}

func (s *bindStore) LastRepo() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Last
}

func (s *bindStore) SetLastRepo(value string) error {
	return s.persist(func() { s.Last = value })
}

func (s *bindStore) IsThread(channel string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Threads[channel]
}

func (s *bindStore) MarkThread(conv, parent string) error {
	return s.persist(func() {
		s.Threads[conv] = true
		if parent != "" {
			s.Parents[conv] = parent
		}
	})
}

func (s *bindStore) Bind(channel, session string) error {
	return s.persist(func() { s.Channels[channel] = session })
}

func (s *bindStore) BindThread(slot, conv, parent, session string) error {
	return s.persist(func() {
		s.Channels[slot] = session
		s.Threads[conv] = true
		if parent != "" {
			s.Parents[conv] = parent
		}
	})
}

func (s *bindStore) Children(parent string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for child, p := range s.Parents {
		if p == parent {
			out = append(out, child)
		}
	}
	return out
}

func (s *bindStore) Forget(channel string) error {
	return s.persist(func() {
		for slot := range s.Channels {
			if convOfSlot(slot) == channel {
				delete(s.Channels, slot)
			}
		}
		delete(s.Threads, channel)
		delete(s.Levels, channel)
		delete(s.Modes, channel)
		delete(s.Repos, channel)
		delete(s.Parents, channel)
	})
}

func (s *bindStore) Unbind(channel string) error {
	return s.persist(func() { delete(s.Channels, channel) })
}

func (s *bindStore) UnbindAll(conv string) error {
	return s.persist(func() {
		for slot := range s.Channels {
			if convOfSlot(slot) == conv {
				delete(s.Channels, slot)
			}
		}
	})
}
