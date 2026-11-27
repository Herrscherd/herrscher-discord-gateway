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

type renderClient interface {
	TextLimit(channelID string) int
	UpsertStatusMessage(ctx context.Context, channelID, messageID, content string) (string, error)
	Post(ctx context.Context, channelID, content string) error
	React(ctx context.Context, channelID, messageID, emoji string) error
	Unreact(ctx context.Context, channelID, messageID, emoji string) error
	Delete(ctx context.Context, channelID, messageID string) error
}

type sink struct {
	run       lifetime
	rc        renderClient
	ch        string
	level     func() string
	owner     string
	tidy      bool
	base      messages
	msgs      messages
	localized bool
	langs     sessionLanguages
	log       *slog.Logger

	mu       sync.Mutex
	queue    []*sinkJob
	draining bool

	pv       *progressView
	lastUser msgRef
	acked    msgRef
}

type msgRef struct{ ch, id, author string }

func newSink(run lifetime, renderer renderClient, channel string, level func() string, owner string, tidy bool, msgs messages, log *slog.Logger) *sink {
	if level == nil {
		level = staticLevel("")
	}
	return &sink{run: run, rc: renderer, ch: channel, level: level, owner: owner, tidy: tidy, base: msgs, msgs: msgs, log: log}
}

func (s *sink) localize() {
	if s.localized {
		return
	}
	s.localized = true
	s.msgs = s.langs.messagesFor(s.ch, s.base)
	if s.pv != nil {
		s.pv.msgs = s.msgs
	}
}

func (s *sink) endTurn() {
	s.localized = false
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
	msgs  messages
	langs sessionLanguages
	log   *slog.Logger

	mu sync.Mutex
	m  map[string]*sink
}

func newSinks(run lifetime, renderer renderClient, level func(conv string) string, owner string, tidy bool, msgs messages, log *slog.Logger) *sinks {
	if level == nil {
		level = staticLevels("")
	}
	return &sinks{run: run, rc: renderer, level: level, owner: owner, tidy: tidy, msgs: msgs, log: log, m: make(map[string]*sink)}
}

func (s *sinks) at(convID string) *sink {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := s.m[convID]; v != nil {
		return v
	}
	created := newSink(s.run, s.rc, convID, func() string { return s.level(convID) }, s.owner, s.tidy, s.msgs, s.log)
	created.langs = s.langs
	s.m[convID] = created
	return created
}

func (s *sinks) drop(convID string) {
	s.mu.Lock()
	delete(s.m, convID)
	s.mu.Unlock()
}

func (s *sink) noteUser(ctx context.Context, channelID, messageID, author string) {
	ref := msgRef{ch: channelID, id: messageID, author: author}
	s.dispatch(ctx, "note", func() {
		s.lastUser = ref
	})
}

func (s *sink) ack(ctx context.Context, channelID, messageID, author string) {
	ref := msgRef{ch: channelID, id: messageID, author: author}
	s.dispatch(ctx, "ack", func() {
		s.lastUser = ref
		if messageID == "" || s.acked == ref {
			return
		}
		s.addAck(ctx, ref)
	})
}

func (s *sink) addAck(ctx context.Context, ref msgRef) {
	ack := s.msgs.head(headAck)
	if ack == "" {
		return
	}
	if err := s.rc.React(ctx, ref.chOr(s.ch), ref.id, ack); err != nil {
		s.log.Warn("add ack reaction failed", "err", err)
		return
	}
	s.acked = ref
}

func (s *sink) fail(ctx context.Context, reason string) {
	s.dispatch(ctx, "fail", func() {
		s.applyFail(ctx, reason)
	})
}

func (s *sink) applyFail(ctx context.Context, reason string) {
	if s.pv != nil {
		s.pv.flush(true)
		s.pv = nil
	}
	channel := s.acked.chOr(s.ch)
	s.swapAck(ctx, channel, headFail)
	if reason == "" {
		return
	}
	s.postParts(ctx, channel, s.mention(withEmoji(s.msgs.head(headFail), reason)))
}

const humanKind = "event:human"

func (s *sink) handle(event contracts.Event) {
	ctx := s.run()
	s.dispatch(ctx, "event:"+event.T, func() {
		s.apply(ctx, event)
	})
}

func (s *sink) apply(ctx context.Context, event contracts.Event) {
	switch event.T {
	case "human":
		s.onHuman(ctx)
	case "status":
		s.onStatus(event.Text)
	case "chunk":
		s.onChunk(event.Text)
	case "reset":
		s.onReset()
	case "abandoned":
		s.onAbandoned(ctx)
		s.endTurn()
	case "tool_done":
		s.onToolDone(event.ToolCall)
	case "reply":
		s.onReply(ctx, event)
		if event.Done {
			s.endTurn()
		}
	}
}

func (s *sink) onHuman(ctx context.Context) {
	if level := s.level(); rendersProgress(level) {
		s.pv = newProgressView(s.upsertStatus, level, time.Now(), s.msgs, s.log)
		s.pv.maxLen = s.rc.TextLimit(s.ch)
	}
	if s.lastUser.id != "" && s.acked != s.lastUser {
		s.addAck(ctx, s.lastUser)
	}
}

func (s *sink) upsertStatus(id, content string) (string, error) {
	return s.rc.UpsertStatusMessage(s.run(), s.ch, id, content)
}

func (s *sink) onStatus(text string) {
	if s.pv == nil {
		return
	}
	s.pv.add(toolEvent(text))
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

func (s *sink) onAbandoned(ctx context.Context) {
	if s.pv != nil {
		s.pv.flush(true)
		s.pv = nil
	}
	s.clearAck(ctx, s.ch)
}

func (s *sink) onToolDone(call *contracts.ToolCall) {
	if s.pv == nil || call == nil || call.State != contracts.ToolCallFailed {
		return
	}
	s.pv.add(contracts.BackendEvent{Kind: "tool_failed", Tool: call.Tool})
}

func (s *sink) onReply(ctx context.Context, event contracts.Event) {
	if !event.Done {
		return
	}
	ending := endingOf(s.msgs, event.Outcome)
	channel := s.ch
	s.postParts(ctx, channel, s.answer(ending, event.Text))
	if s.pv != nil {
		if event.Cost > 0 {
			s.pv.add(contracts.BackendEvent{Kind: "result", Cost: event.Cost})
		}
		s.pv.finish(ending)
		s.pv = nil
	}
	if ending.reaction == "" && s.tidyPing(ctx, channel) {
		return
	}
	s.swapAck(ctx, channel, ending.reaction)
}

func (s *sink) postParts(ctx context.Context, channelID, text string) {
	for _, part := range chunkText(text, s.rc.TextLimit(channelID)) {
		if err := s.rc.Post(ctx, channelID, part); err != nil {
			s.log.Warn("post message part failed", "err", err)
		}
	}
}

func (s *sink) tidyPing(ctx context.Context, channelID string) bool {
	if !s.tidy || s.acked.id == "" || s.acked.chOr(channelID) != channelID {
		return false
	}
	if s.owner == "" || s.acked.author != s.owner {
		return false
	}
	if err := s.rc.Delete(ctx, channelID, s.acked.id); err != nil {
		s.log.Warn("delete the opening ping failed", "err", err)
		return false
	}
	gone := s.acked
	s.acked = msgRef{}
	if s.lastUser == gone {
		s.lastUser = msgRef{}
	}
	return true
}

func (s *sink) answer(ending turnEnding, text string) string {
	if text != "" {
		return s.mention(withEmoji(s.msgs.head(ending.prefix), text))
	}
	if !ending.announce {
		return ""
	}
	return s.mention(withEmoji(s.msgs.head(ending.summary), ending.label))
}

func (s *sink) mention(text string) string {
	if s.owner == "" {
		return text
	}
	return "<@" + s.owner + "> " + text
}

func (s *sink) swapAck(ctx context.Context, channel string, reaction headKey) {
	if s.acked.id == "" {
		return
	}
	acked := s.acked
	s.clearAck(ctx, channel)
	if reaction == "" {
		return
	}
	emoji := s.msgs.head(reaction)
	if emoji == "" {
		return
	}
	if err := s.rc.React(ctx, channel, acked.id, emoji); err != nil {
		s.log.Warn("add outcome reaction failed", "err", err)
	}
}

func (s *sink) clearAck(ctx context.Context, channelID string) {
	if s.acked.id == "" {
		return
	}
	if err := s.rc.Unreact(ctx, s.acked.chOr(channelID), s.acked.id, s.msgs.head(headAck)); err != nil {
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

func toolEvent(text string) contracts.BackendEvent {
	name, detail, _ := strings.Cut(strings.TrimSpace(text), " ")
	return contracts.BackendEvent{Kind: "tool", Tool: name, Detail: strings.TrimSpace(detail)}
}

func chunkText(text string, limit int) []string {
	var out []string
	runes := []rune(text)
	for len(runes) > limit {
		cut := limit
		for index := limit - 1; index > limit/2; index-- {
			if runes[index] == '\n' {
				cut = index
				break
			}
		}
		out = append(out, string(runes[:cut]))
		runes = runes[cut:]
		if len(runes) > 0 && runes[0] == '\n' {
			runes = runes[1:]
		}
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}
