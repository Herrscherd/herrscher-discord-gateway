package discord

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

type bindStore struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	path    string
	locked  error
	write   func(path string, data []byte) error

	Channels map[string]string `json:"channels"`
	Threads  map[string]bool   `json:"threads,omitempty"`
	Levels   map[string]string `json:"levels,omitempty"`
	Modes    map[string]string `json:"modes,omitempty"`
	Repos    map[string]string `json:"repos,omitempty"`
	Parents  map[string]string `json:"parents,omitempty"`
	Last     string            `json:"lastRepo,omitempty"`
}

func newBindStore(path string, log *slog.Logger) *bindStore {
	store := &bindStore{path: path, write: writeAtomic}
	if err := loadStoreFile(path, store); err != nil {
		store.resetMaps()
		store.locked = fmt.Errorf("%w: %w", errStoreLocked, err)
		log.Warn("bind store unreadable or corrupt, binding is refused until the file is repaired", "path", path, "err", err)
	}
	store.ensureMaps()
	return store
}

func (s *bindStore) resetMaps() {
	s.Channels, s.Threads, s.Levels = make(map[string]string), make(map[string]bool), make(map[string]string)
	s.Modes, s.Repos, s.Parents = make(map[string]string), make(map[string]string), make(map[string]string)
	s.Last = ""
}

func (s *bindStore) ensureMaps() {
	if s.Channels == nil {
		s.Channels = make(map[string]string)
	}
	if s.Threads == nil {
		s.Threads = make(map[string]bool)
	}
	if s.Levels == nil {
		s.Levels = make(map[string]string)
	}
	if s.Modes == nil {
		s.Modes = make(map[string]string)
	}
	if s.Repos == nil {
		s.Repos = make(map[string]string)
	}
	if s.Parents == nil {
		s.Parents = make(map[string]string)
	}
}

func (s *bindStore) persist(mutate func()) error {
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
