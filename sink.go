package discord

import (
	"context"
	"strings"
	"sync"
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const gatewayMaxLen = 2000

const ackEmoji = "⏳"

const failEmoji = "❌"

type renderClient interface {
	UpsertStatusMessage(ctx context.Context, channelID, messageID, content string) (string, error)
	Post(ctx context.Context, channelID, content string) error
	React(ctx context.Context, channelID, messageID, emoji string) error
	Unreact(ctx context.Context, channelID, messageID, emoji string) error
	Delete(ctx context.Context, channelID, messageID string) error
}

type sink struct {
	ctx   context.Context
	rc    renderClient
	ch    string
	level func() string
	owner string
	tidy  bool

	mu       sync.Mutex
	pv       *progressView
	lastUser msgRef
	acked    msgRef
}

type msgRef struct{ ch, id, author string }

func newSink(ctx context.Context, rc renderClient, ch string, level func() string, owner string, tidy bool) *sink {
	if level == nil {
		level = staticLevel("")
	}
	return &sink{ctx: ctx, rc: rc, ch: ch, level: level, owner: owner, tidy: tidy}
}

func staticLevel(level string) func() string {
	level = verbositySetting(level)
	return func() string { return level }
}

func staticLevels(level string) func(string) string {
	level = verbositySetting(level)
	return func(string) string { return level }
}

type sinks struct {
	ctx   context.Context
	rc    renderClient
	level func(conv string) string
	owner string
	tidy  bool

	mu sync.Mutex
	m  map[string]*sink
}

func newSinks(ctx context.Context, rc renderClient, level func(conv string) string, owner string, tidy bool) *sinks {
	if level == nil {
		level = staticLevels("")
	}
	return &sinks{ctx: ctx, rc: rc, level: level, owner: owner, tidy: tidy, m: map[string]*sink{}}
}

func (s *sinks) at(convID string) *sink {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := s.m[convID]; v != nil {
		return v
	}
	v := newSink(s.ctx, s.rc, convID, func() string { return s.level(convID) }, s.owner, s.tidy)
	s.m[convID] = v
	return v
}

func (s *sinks) drop(convID string) {
	s.mu.Lock()
	delete(s.m, convID)
	s.mu.Unlock()
}

func (s *sink) noteUser(ch, id, author string) {
	s.mu.Lock()
	s.lastUser = msgRef{ch: ch, id: id, author: author}
	s.mu.Unlock()
}

func (s *sink) ack(ch, id, author string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref := msgRef{ch: ch, id: id, author: author}
	s.lastUser = ref
	if id == "" || s.acked == ref {
		return
	}
	if err := s.rc.React(s.ctx, ch, id, ackEmoji); err == nil {
		s.acked = ref
	}
}

func (s *sink) fail(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pv != nil {
		s.pv.flush(true)
		s.pv = nil
	}
	ch := s.acked.chOr(s.ch)
	if s.acked.id != "" {
		_ = s.rc.Unreact(s.ctx, ch, s.acked.id, ackEmoji)
		_ = s.rc.React(s.ctx, ch, s.acked.id, failEmoji)
		s.acked = msgRef{}
	}
	if reason == "" {
		return
	}
	for _, part := range chunkText(s.answer(failEmoji+" "+reason), gatewayMaxLen) {
		_ = s.rc.Post(s.ctx, ch, part)
	}
}

func (s *sink) handle(e contracts.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.ch

	switch e.T {
	case "human":
		if level := s.level(); rendersProgress(level) {
			post := func(id, content string) (string, error) {
				return s.rc.UpsertStatusMessage(s.ctx, ch, id, content)
			}
			s.pv = newProgressView(post, level, time.Now())
		}
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
		if s.pv != nil {
			s.pv.reset()
		}
	case "abandoned":
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
		if s.tidyPing(ch) {
			return
		}
		s.clearAck(ch)
	}
}

func (s *sink) tidyPing(ch string) bool {
	if !s.tidy || s.acked.id == "" || s.acked.chOr(ch) != ch {
		return false
	}
	if s.owner == "" || s.acked.author != s.owner {
		return false
	}
	if err := s.rc.Delete(s.ctx, ch, s.acked.id); err != nil {
		return false
	}
	gone := s.acked
	s.acked = msgRef{}
	if s.lastUser == gone {
		s.lastUser = msgRef{}
	}
	return true
}

func (s *sink) answer(text string) string {
	if text == "" {
		text = "✅ terminé"
	}
	if s.owner == "" {
		return text
	}
	return "<@" + s.owner + "> " + text
}

func (s *sink) clearAck(ch string) {
	if s.acked.id == "" {
		return
	}
	_ = s.rc.Unreact(s.ctx, s.acked.chOr(ch), s.acked.id, ackEmoji)
	s.acked = msgRef{}
}

func (m msgRef) chOr(def string) string {
	if m.ch == "" {
		return def
	}
	return m.ch
}

func splitTool(s string) (tool, detail string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
}

func chunkText(s string, max int) []string {
	var out []string
	r := []rune(s)
	for len(r) > max {
		cut := max
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
