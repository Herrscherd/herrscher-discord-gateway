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
	ctx   context.Context
	rc    renderClient
	ch    string // the conversation this sink renders into
	level string

	mu       sync.Mutex
	pv       *progressView
	lastUser string // id of the message that triggered the current/next turn
	acked    string // id currently carrying the ⏳ reaction ("" if none)
}

func newSink(ctx context.Context, rc renderClient, ch, level string) *sink {
	if level == "" {
		level = "quiet"
	}
	return &sink{ctx: ctx, rc: rc, ch: ch, level: level}
}

// sinks is the set of live per-conversation renderers. The gateway is no longer
// mono-channel: one sink per conversation, each with its own in-flight turn
// state, created on first use and kept for the process's life (a conversation
// the bot has spoken in is cheap to keep, and its ack/progress ids must survive
// between turns).
type sinks struct {
	ctx   context.Context
	rc    renderClient
	level string

	mu sync.Mutex
	m  map[string]*sink
}

func newSinks(ctx context.Context, rc renderClient, level string) *sinks {
	return &sinks{ctx: ctx, rc: rc, level: level, m: map[string]*sink{}}
}

// at returns the renderer for one conversation, creating it on first use.
func (s *sinks) at(convID string) *sink {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := s.m[convID]; v != nil {
		return v
	}
	v := newSink(s.ctx, s.rc, convID, s.level)
	s.m[convID] = v
	return v
}

// noteUser records the id of the latest user (non-bot) message, so the next
// turn's ACK reaction lands on it.
func (s *sink) noteUser(id string) {
	s.mu.Lock()
	s.lastUser = id
	s.mu.Unlock()
}

// ack marks a message as received now, without waiting for a turn to start.
// Some pings are answered by a question rather than by work — the repo menu —
// and there is no "human" event to hang the ⏳ on, so the ping would sit
// unmarked while the operator decides, reading as ignored.
func (s *sink) ack(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUser = id
	if id == "" || s.acked == id {
		return
	}
	if err := s.rc.React(s.ctx, s.ch, id, ackEmoji); err == nil {
		s.acked = id
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
		post := func(id, content string) (string, error) {
			return s.rc.UpsertStatusMessage(s.ctx, ch, id, content)
		}
		s.pv = newProgressView(post, s.level, time.Now())
		// Already marked when the ping was received (see ack): reacting twice
		// would be a wasted call, and the same ⏳ already says "received".
		if s.lastUser != "" && s.acked != s.lastUser {
			if err := s.rc.React(s.ctx, ch, s.lastUser, ackEmoji); err == nil {
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
		if e.Text != "" {
			for _, part := range chunkText(e.Text, gatewayMaxLen) {
				_ = s.rc.Post(s.ctx, ch, part)
			}
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

// clearAck removes the ⏳ reaction left on the triggering message, if any.
func (s *sink) clearAck(ch string) {
	if s.acked == "" {
		return
	}
	_ = s.rc.Unreact(s.ctx, ch, s.acked, ackEmoji)
	s.acked = ""
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
