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
	s := &bindStore{path: path, Channels: map[string]string{}, Threads: map[string]bool{}, Levels: map[string]string{}, Modes: map[string]string{}, Repos: map[string]string{}, Parents: map[string]string{}}
	if data, err := os.ReadFile(filepath.Clean(path)); err == nil {
		if err := json.Unmarshal(data, s); err != nil {
			log.Warn("bind store corrupt, ignoring it", "path", path, "err", err)
			s.Channels, s.Threads, s.Levels = map[string]string{}, map[string]bool{}, map[string]string{}
			s.Modes, s.Repos, s.Parents = map[string]string{}, map[string]string{}, map[string]string{}
			s.Last = ""
		}
		if s.Channels == nil {
			s.Channels = map[string]string{}
		}
		if s.Threads == nil {
			s.Threads = map[string]bool{}
		}
		if s.Levels == nil {
			s.Levels = map[string]string{}
		}
		if s.Modes == nil {
			s.Modes = map[string]string{}
		}
		if s.Repos == nil {
			s.Repos = map[string]string{}
		}
		if s.Parents == nil {
			s.Parents = map[string]string{}
		}
	}
	return s
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
