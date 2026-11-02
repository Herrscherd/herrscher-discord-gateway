package discord

import (
	"context"
	"log/slog"
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
	run   lifetime
	rc    renderClient
	ch    string
	level func() string
	owner string
	tidy  bool
	log   *slog.Logger

	mu       sync.Mutex
	pv       *progressView
	lastUser msgRef
	acked    msgRef
}

type msgRef struct{ ch, id, author string }

func newSink(run lifetime, rc renderClient, ch string, level func() string, owner string, tidy bool, log *slog.Logger) *sink {
	if level == nil {
		level = staticLevel("")
	}
	return &sink{run: run, rc: rc, ch: ch, level: level, owner: owner, tidy: tidy, log: log}
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
	run   lifetime
	rc    renderClient
	level func(conv string) string
	owner string
	tidy  bool
	log   *slog.Logger

	mu sync.Mutex
	m  map[string]*sink
}

func newSinks(run lifetime, rc renderClient, level func(conv string) string, owner string, tidy bool, log *slog.Logger) *sinks {
	if level == nil {
		level = staticLevels("")
	}
	return &sinks{run: run, rc: rc, level: level, owner: owner, tidy: tidy, log: log, m: map[string]*sink{}}
}

func (s *sinks) at(convID string) *sink {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := s.m[convID]; v != nil {
		return v
	}
	v := newSink(s.run, s.rc, convID, func() string { return s.level(convID) }, s.owner, s.tidy, s.log)
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
	s.addAck(ref)
}

func (s *sink) addAck(ref msgRef) {
	if err := s.rc.React(s.run(), ref.chOr(s.ch), ref.id, ackEmoji); err != nil {
		s.log.Warn("add ack reaction failed", "err", err)
		return
	}
	s.acked = ref
}

func (s *sink) fail(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pv != nil {
		s.pv.flush(true)
		s.pv = nil
	}
	ctx := s.run()
	ch := s.acked.chOr(s.ch)
	if s.acked.id != "" {
		s.clearAck(ch)
		if err := s.rc.React(ctx, ch, s.acked.id, failEmoji); err != nil {
			s.log.Warn("add failure reaction failed", "err", err)
		}
		s.acked = msgRef{}
	}
	if reason == "" {
		return
	}
	s.postParts(ctx, ch, s.answer(failEmoji+" "+reason))
}

func (s *sink) handle(e contracts.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch e.T {
	case "human":
		s.onHuman()
	case "status":
		s.onStatus(e.Text)
	case "chunk":
		s.onChunk(e.Text)
	case "reset":
		s.onReset()
	case "abandoned":
		s.onAbandoned()
	case "reply":
		s.onReply(e)
	}
}

func (s *sink) onHuman() {
	if level := s.level(); rendersProgress(level) {
		s.pv = newProgressView(s.upsertStatus, level, time.Now(), s.log)
	}
	if s.lastUser.id != "" && s.acked != s.lastUser {
		s.addAck(s.lastUser)
	}
}

func (s *sink) upsertStatus(id, content string) (string, error) {
	return s.rc.UpsertStatusMessage(s.run(), s.ch, id, content)
}

func (s *sink) onStatus(text string) {
	if s.pv == nil {
		return
	}
	tool, detail := splitTool(text)
	s.pv.add(contracts.BackendEvent{Kind: "tool", Tool: tool, Detail: detail})
}

func (s *sink) onChunk(text string) {
	if s.pv != nil {
		s.pv.add(contracts.BackendEvent{Kind: "text", Detail: text})
	}
}

func (s *sink) onReset() {
	if s.pv != nil {
		s.pv.reset()
	}
}

func (s *sink) onAbandoned() {
	if s.pv != nil {
		s.pv.flush(true)
		s.pv = nil
	}
	s.clearAck(s.ch)
}

func (s *sink) onReply(e contracts.Event) {
	if !e.Done {
		return
	}
	ch := s.ch
	s.postParts(s.run(), ch, s.answer(e.Text))
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

func (s *sink) postParts(ctx context.Context, ch, text string) {
	for _, part := range chunkText(text, gatewayMaxLen) {
		if err := s.rc.Post(ctx, ch, part); err != nil {
			s.log.Warn("post message part failed", "err", err)
		}
	}
}

func (s *sink) tidyPing(ch string) bool {
	if !s.tidy || s.acked.id == "" || s.acked.chOr(ch) != ch {
		return false
	}
	if s.owner == "" || s.acked.author != s.owner {
		return false
	}
	if err := s.rc.Delete(s.run(), ch, s.acked.id); err != nil {
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
	if err := s.rc.Unreact(s.run(), s.acked.chOr(ch), s.acked.id, ackEmoji); err != nil {
		s.log.Warn("remove ack reaction failed", "err", err)
	}
	s.acked = msgRef{}
}

func (m msgRef) chOr(def string) string {
	if m.ch == "" {
		return def
	}
	return m.ch
}

func splitTool(s string) (string, string) {
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
