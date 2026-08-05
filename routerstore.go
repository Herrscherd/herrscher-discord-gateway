package discord

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// bindStore holds what the gateway remembers about a conversation — which
// session drives it, whether the gateway opened it, how loud it wants the bot —
// persisted as JSON beside the allow store. It lives entirely in the plugin: the
// core knows sessions, never channels-to-sessions. Losing it is safe — an
// unbound channel simply asks the operator again.
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
	// Levels holds the render level `/verbosity` set on a conversation, which
	// overrides the daemon-wide DISCORD_VERBOSITY default. It is keyed by
	// conversation and not by session on purpose: how loud the bot is belongs to
	// the room it speaks in — often a channel other people read — not to whatever
	// job happens to be running there.
	Levels map[string]string `json:"levels,omitempty"`
	// Modes holds the `/mode` a channel is in. Only "support" is a mode; an
	// absent entry is the ordinary channel, where one conversation is bound to
	// the channel itself. It is keyed by channel and never by thread: the mode
	// describes a room where independent questions arrive, and the threads it
	// opens are ordinary conversations once opened.
	Modes map[string]string `json:"modes,omitempty"`
	// Repos holds the repo a support channel works on, as the menu value the
	// operator picked. A support channel opens a session per ping, and asking
	// every asker which repo — a question they often cannot answer — is friction
	// the first answer already resolved for the room.
	Repos map[string]string `json:"repos,omitempty"`
}

func newBindStore(path string) *bindStore {
	s := &bindStore{path: path, Channels: map[string]string{}, Threads: map[string]bool{}, Levels: map[string]string{}, Modes: map[string]string{}, Repos: map[string]string{}}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, s); err != nil {
			// A corrupt store must not silently resolve to a wrong session: report
			// it and start empty, which only costs one extra question.
			// Every map goes, not just the bindings: a half-decoded Threads would
			// leave a channel marked as a thread the gateway opened, and there a
			// plain message needs no @mention to make the bot act.
			fmt.Fprintf(os.Stderr, "discord gateway: bind store %s is corrupt, ignoring: %v\n", path, err)
			s.Channels, s.Threads, s.Levels = map[string]string{}, map[string]bool{}, map[string]string{}
			s.Modes, s.Repos = map[string]string{}, map[string]string{}
		}
		if s.Channels == nil {
			s.Channels = map[string]string{}
		}
		// Absent from every store written before threads existed.
		if s.Threads == nil {
			s.Threads = map[string]bool{}
		}
		// Likewise for every store written before /verbosity existed.
		if s.Levels == nil {
			s.Levels = map[string]string{}
		}
		// Likewise for every store written before /mode existed. A nil map here
		// would panic the first time a channel is put in support mode.
		if s.Modes == nil {
			s.Modes = map[string]string{}
		}
		if s.Repos == nil {
			s.Repos = map[string]string{}
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

// Level returns the render level set on a conversation, or "" when none is — the
// caller then falls back on the configured default.
func (s *bindStore) Level(channel string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Levels[channel]
}

// SetLevel overrides the render level of one conversation. An empty level drops
// the override rather than storing one, putting the conversation back on the
// configured default.
func (s *bindStore) SetLevel(channel, level string) error {
	return s.persist(func() {
		if level == "" {
			delete(s.Levels, channel)
			return
		}
		s.Levels[channel] = level
	})
}

// Mode returns the mode a channel is in, or "" for the ordinary one.
func (s *bindStore) Mode(channel string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Modes[channel]
}

// SetMode puts a channel in a mode. An empty mode drops the entry, putting the
// channel back to the ordinary behaviour rather than storing a second name for
// it.
func (s *bindStore) SetMode(channel, mode string) error {
	return s.persist(func() {
		if mode == "" {
			delete(s.Modes, channel)
			return
		}
		s.Modes[channel] = mode
	})
}

// Repo returns the repo a channel works on, as a menu value, or "" when the
// question has not been answered here yet.
func (s *bindStore) Repo(channel string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Repos[channel]
}

// SetRepo remembers what a channel works on, so the question is asked once for
// the room rather than once per conversation opened in it.
func (s *bindStore) SetRepo(channel, value string) error {
	return s.persist(func() {
		if value == "" {
			delete(s.Repos, channel)
			return
		}
		s.Repos[channel] = value
	})
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
