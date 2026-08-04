package discord

import (
	"context"
	"strings"
	"sync"
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

// gatewayMaxLen is Discord's hard per-message limit; long replies are chunked.
const gatewayMaxLen = 2000

// ackEmoji marks a received turn on the triggering user message; removed when
// the turn finishes.
const ackEmoji = "⏳"

// renderClient is the narrow Discord surface the sink needs (faked in tests).
// It has no DefaultChannel: a sink is told which conversation it renders into,
// so nothing here can fall back to a single global channel.
type renderClient interface {
	UpsertStatusMessage(ctx context.Context, channelID, messageID, content string) (string, error)
	Post(ctx context.Context, channelID, content string) error
	React(ctx context.Context, channelID, messageID, emoji string) error
	Unreact(ctx context.Context, channelID, messageID, emoji string) error
}

// sink renders the live turn-event stream into ONE Discord conversation: one
// in-flight turn at a time, guarded by mu. Several sinks coexist (see sinks),
// one per conversation the bot is talking in.
type sink struct {
	ctx context.Context
	rc  renderClient
	ch  string // the conversation this sink renders into
	// level is resolved at the start of each turn rather than fixed at
	// construction: `/verbosity` changes how loud a conversation is while its
	// sink is alive, and a sink outlives every turn it renders.
	level func() string
	owner string // the operator every answer pings

	mu       sync.Mutex
	pv       *progressView
	lastUser msgRef // the message that triggered the current/next turn
	acked    msgRef // the message currently carrying the ⏳ reaction
}

// msgRef points at one message. The channel travels with the id because a sink
// does not always render where its trigger lives: a job moved into a private
// thread renders there, while the ping that opened it stays in the channel it
// was written in, and reacting to it in the wrong channel is a 404.
type msgRef struct{ ch, id string }

func newSink(ctx context.Context, rc renderClient, ch string, level func() string, owner string) *sink {
	if level == nil {
		level = staticLevel("")
	}
	return &sink{ctx: ctx, rc: rc, ch: ch, level: level, owner: owner}
}

// staticLevel resolves to one fixed render level, normalized. It is what a
// conversation that nobody can retune uses — tests, and the fallback when no
// resolver was wired.
func staticLevel(level string) func() string {
	level = verbositySetting(level)
	return func() string { return level }
}

// staticLevels resolves every conversation to the same fixed render level.
func staticLevels(level string) func(string) string {
	level = verbositySetting(level)
	return func(string) string { return level }
}

// sinks is the set of live per-conversation renderers. The gateway is no longer
// mono-channel: one sink per conversation, each with its own in-flight turn
// state, created on first use and kept for the process's life (a conversation
// the bot has spoken in is cheap to keep, and its ack/progress ids must survive
// between turns).
type sinks struct {
	ctx context.Context
	rc  renderClient
	// level answers "how loud is this conversation" for any conversation id. It
	// is a function, not a value, because the answer is the operator's to change
	// at any moment (see sink.level).
	level func(conv string) string
	owner string

	mu sync.Mutex
	m  map[string]*sink
}

func newSinks(ctx context.Context, rc renderClient, level func(conv string) string, owner string) *sinks {
	if level == nil {
		level = staticLevels("")
	}
	return &sinks{ctx: ctx, rc: rc, level: level, owner: owner, m: map[string]*sink{}}
}

// at returns the renderer for one conversation, creating it on first use.
func (s *sinks) at(convID string) *sink {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := s.m[convID]; v != nil {
		return v
	}
	v := newSink(s.ctx, s.rc, convID, func() string { return s.level(convID) }, s.owner)
	s.m[convID] = v
	return v
}

// noteUser records the latest user (non-bot) message, so the next turn's ACK
// reaction lands on it.
func (s *sink) noteUser(ch, id string) {
	s.mu.Lock()
	s.lastUser = msgRef{ch: ch, id: id}
	s.mu.Unlock()
}

// ack marks a message as received now, without waiting for a turn to start.
// Some pings are answered by a question rather than by work — the repo menu —
// and there is no "human" event to hang the ⏳ on, so the ping would sit
// unmarked while the operator decides, reading as ignored.
func (s *sink) ack(ch, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref := msgRef{ch: ch, id: id}
	s.lastUser = ref
	if id == "" || s.acked == ref {
		return
	}
	if err := s.rc.React(s.ctx, ch, id, ackEmoji); err == nil {
		s.acked = ref
	}
}

// handle renders one live turn event onto Discord. It holds s.mu across the
// event's blocking REST I/O (React, UpsertStatusMessage, Post, Unreact) on
// purpose: one turn at a time per conversation, so the inbound goroutine's
// noteUser briefly serializes behind a single event's render — acceptable and
// intentional for this single-in-flight-turn design. Other conversations render
// on their own sink and are never blocked by this one.
func (s *sink) handle(e contracts.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.ch

	switch e.T {
	case "human":
		// At the silent level there is no live view at all: no progress message
		// is ever posted, so every "if s.pv != nil" below is skipped and the turn
		// shows up as the ⏳ and then the answer.
		if level := s.level(); rendersProgress(level) {
			post := func(id, content string) (string, error) {
				return s.rc.UpsertStatusMessage(s.ctx, ch, id, content)
			}
			s.pv = newProgressView(post, level, time.Now())
		}
		// Already marked when the ping was received (see ack): reacting twice
		// would be a wasted call, and the same ⏳ already says "received".
		if s.lastUser.id != "" && s.acked != s.lastUser {
			if err := s.rc.React(s.ctx, s.lastUser.chOr(ch), s.lastUser.id, ackEmoji); err == nil {
				s.acked = s.lastUser
			}
		}
	case "status":
		if s.pv != nil {
			tool, detail := splitTool(e.Text)
			s.pv.add(contracts.BackendEvent{Kind: "tool", Tool: tool, Detail: detail})
		}
	case "chunk":
		if s.pv != nil {
			s.pv.add(contracts.BackendEvent{Kind: "text", Detail: e.Text})
		}
	case "reset":
		// A backend crash+retry discards the partial turn, but the SAME turn
		// continues (the prompt is resent). Drop the partial render in place and
		// keep the ⏳ ACK so the retried turn keeps updating the live message,
		// rather than stranding it on a misleading summary and going dark.
		if s.pv != nil {
			s.pv.reset()
		}
	case "abandoned":
		// The turn ended without a reply (backend/bridge dropped, or the daemon
		// is shutting down). Clear the ⏳ ACK so the message no longer reads as
		// pending and drop the live view so the next turn starts fresh. The
		// partial progress message stays as the last honest state of the turn —
		// the host left the choice of presentation to us. Force a final flush
		// first so lines coalesced inside the throttle window are not lost.
		if s.pv != nil {
			s.pv.flush(true)
			s.pv = nil
		}
		s.clearAck(ch)
	case "reply":
		if !e.Done {
			return
		}
		for _, part := range chunkText(s.answer(e.Text), gatewayMaxLen) {
			_ = s.rc.Post(s.ctx, ch, part)
		}
		if s.pv != nil {
			if e.Cost > 0 {
				s.pv.add(contracts.BackendEvent{Kind: "result", Cost: e.Cost})
			}
			s.pv.finish()
			s.pv = nil
		}
		s.clearAck(ch)
	}
}

// answer prepares the text a finished turn is posted as. It opens with the
// operator's @mention: a turn runs for minutes and its answer is a plain post
// in whatever conversation the job lives in — without a mention Discord tells
// nobody, and the operator has to go looking. A turn that ends with nothing to
// say still gets a line, because the alternative is the ⏳ silently vanishing,
// which reads exactly like a bot that died.
func (s *sink) answer(text string) string {
	if text == "" {
		text = "✅ terminé"
	}
	if s.owner == "" {
		return text
	}
	return "<@" + s.owner + "> " + text
}

// clearAck removes the ⏳ reaction left on the triggering message, if any.
func (s *sink) clearAck(ch string) {
	if s.acked.id == "" {
		return
	}
	_ = s.rc.Unreact(s.ctx, s.acked.chOr(ch), s.acked.id, ackEmoji)
	s.acked = msgRef{}
}

// chOr falls back to the sink's own conversation for a ref recorded without a
// channel of its own.
func (m msgRef) chOr(def string) string {
	if m.ch == "" {
		return def
	}
	return m.ch
}

// splitTool recovers the tool name and detail from a status line emitted as
// "Tool Detail" so the progress view can group and icon by tool name.
func splitTool(s string) (tool, detail string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
}

// chunkText splits s into pieces of at most max runes, preferring a newline
// break within the limit. It counts and slices in rune space so multibyte
// runes (accents, "…") are never split into invalid UTF-8 and the 2000-rune
// limit is measured in characters, not bytes.
func chunkText(s string, max int) []string {
	var out []string
	r := []rune(s)
	for len(r) > max {
		cut := max
		// Prefer the last newline within the limit, but only past the
		// halfway point so a stray early newline does not yield tiny chunks.
		for i := max - 1; i > max/2; i-- {
			if r[i] == '\n' {
				cut = i
				break
			}
		}
		out = append(out, string(r[:cut]))
		r = r[cut:]
		if len(r) > 0 && r[0] == '\n' {
			r = r[1:]
		}
	}
	if len(r) > 0 {
		out = append(out, string(r))
	}
	return out
}
