package discord

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// bindStore remembers which session drives which channel, persisted as JSON
// beside the allow store. It lives entirely in the plugin: the core knows
// sessions, never channels-to-sessions. Losing it is safe — an unbound channel
// simply asks the operator again.
type bindStore struct {
	mu   sync.Mutex
	path string

	Channels map[string]string `json:"channels"` // channel id -> session name
}

func newBindStore(path string) *bindStore {
	s := &bindStore{path: path, Channels: map[string]string{}}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, s); err != nil {
			// A corrupt store must not silently resolve to a wrong session: report
			// it and start empty, which only costs one extra question.
			fmt.Fprintf(os.Stderr, "discord gateway: bind store %s is corrupt, ignoring: %v\n", path, err)
			s.Channels = map[string]string{}
		}
		if s.Channels == nil {
			s.Channels = map[string]string{}
		}
	}
	return s
}

// persist mirrors allowStore.persist: mutate and serialize under the lock, write
// outside it so a slow disk cannot block the dispatch hot path.
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

// Session returns the session bound to a channel, or "" when none is.
func (s *bindStore) Session(channel string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Channels[channel]
}

func (s *bindStore) Bind(channel, session string) error {
	return s.persist(func() { s.Channels[channel] = session })
}

func (s *bindStore) Unbind(channel string) error {
	return s.persist(func() { delete(s.Channels, channel) })
}
