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
type renderClient interface {
	DefaultChannel() string
	UpsertStatusMessage(ctx context.Context, channelID, messageID, content string) (string, error)
	Post(ctx context.Context, channelID, content string) error
	React(ctx context.Context, channelID, messageID, emoji string) error
	Unreact(ctx context.Context, channelID, messageID, emoji string) error
}

// sink renders the live turn-event stream onto Discord. Mono-channel: one
// in-flight turn at a time, guarded by mu. It is shared between the Gateway
// (Emit) and the Platform (Read records the last user message id for the ACK).
type sink struct {
	ctx   context.Context
	rc    renderClient
	level string

	mu       sync.Mutex
	pv       *progressView
	lastUser string // id of the message that triggered the current/next turn
	acked    string // id currently carrying the ⏳ reaction ("" if none)
}

func newSink(ctx context.Context, rc renderClient, level string) *sink {
	if level == "" {
		level = "full"
	}
	return &sink{ctx: ctx, rc: rc, level: level}
}

// noteUser records the id of the latest user (non-bot) message, so the next
// turn's ACK reaction lands on it.
func (s *sink) noteUser(id string) {
	s.mu.Lock()
	s.lastUser = id
	s.mu.Unlock()
}

// handle renders one live turn event onto Discord. It holds s.mu across the
// event's blocking REST I/O (React, UpsertStatusMessage, Post, Unreact) on
// purpose: mono-channel means one turn at a time, so the poll goroutine's
// noteUser briefly serializes behind a single event's render, which is
// acceptable and intentional for this single-in-flight-turn design.
func (s *sink) handle(e contracts.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.rc.DefaultChannel()

	switch e.T {
	case "human":
		post := func(id, content string) (string, error) {
			return s.rc.UpsertStatusMessage(s.ctx, ch, id, content)
		}
		s.pv = newProgressView(post, s.level, time.Now())
		if s.lastUser != "" {
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
