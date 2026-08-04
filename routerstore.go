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
	// Threads holds the conversations the gateway opened itself. They are private
	// threads created for one job, holding nobody but the operator and the bot,
	// so a message there needs no @mention to be meant for the bot. Losing this
	// only costs an @: the thread still works, it just stops answering bare
	// messages.
	Threads map[string]bool `json:"threads,omitempty"`
}

func newBindStore(path string) *bindStore {
	s := &bindStore{path: path, Channels: map[string]string{}, Threads: map[string]bool{}}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, s); err != nil {
			// A corrupt store must not silently resolve to a wrong session: report
			// it and start empty, which only costs one extra question.
			// Both maps go, not just the bindings: a half-decoded Threads would
			// leave a channel marked as a thread the gateway opened, and there a
			// plain message needs no @mention to make the bot act.
			fmt.Fprintf(os.Stderr, "discord gateway: bind store %s is corrupt, ignoring: %v\n", path, err)
			s.Channels, s.Threads = map[string]string{}, map[string]bool{}
		}
		if s.Channels == nil {
			s.Channels = map[string]string{}
		}
		// Absent from every store written before threads existed.
		if s.Threads == nil {
			s.Threads = map[string]bool{}
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

// IsThread reports whether this conversation is a thread the gateway opened.
func (s *bindStore) IsThread(channel string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Threads[channel]
}

func (s *bindStore) Bind(channel, session string) error {
	return s.persist(func() { s.Channels[channel] = session })
}

// BindThread binds a conversation the gateway opened for this job alone.
func (s *bindStore) BindThread(channel, session string) error {
	return s.persist(func() {
		s.Channels[channel] = session
		s.Threads[channel] = true
	})
}

// Unbind drops the session a conversation was bound to. The thread flag stays:
// it says what the conversation *is*, not what is running in it, and a private
// thread the gateway opened stays private and two-membered after its session
// dies. Clearing it would make the thread demand an @mention again.
func (s *bindStore) Unbind(channel string) error {
	return s.persist(func() { delete(s.Channels, channel) })
}
