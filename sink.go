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
	queue    []*sinkJob
	draining bool

	pv       *progressView
	lastUser msgRef
	acked    msgRef
}

const sinkDrainBudget = 16

type sinkJob struct {
	kind     string
	step     func()
	done     chan struct{}
	lead     chan struct{}
	waiting  bool
	promoted bool
	finished bool
}

type msgRef struct{ ch, id, author string }

func newSink(run lifetime, renderer renderClient, channel string, level func() string, owner string, tidy bool, log *slog.Logger) *sink {
	if level == nil {
		level = staticLevel("")
	}
	return &sink{run: run, rc: renderer, ch: channel, level: level, owner: owner, tidy: tidy, log: log}
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

func newSinks(run lifetime, renderer renderClient, level func(conv string) string, owner string, tidy bool, log *slog.Logger) *sinks {
	if level == nil {
		level = staticLevels("")
	}
	return &sinks{run: run, rc: renderer, level: level, owner: owner, tidy: tidy, log: log, m: make(map[string]*sink)}
}

func (s *sinks) at(convID string) *sink {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := s.m[convID]; v != nil {
		return v
	}
	created := newSink(s.run, s.rc, convID, func() string { return s.level(convID) }, s.owner, s.tidy, s.log)
	s.m[convID] = created
	return created
}

func (s *sinks) drop(convID string) {
	s.mu.Lock()
	delete(s.m, convID)
	s.mu.Unlock()
}

func (s *sink) submit(ctx context.Context, kind string, step func()) error {
	job := &sinkJob{kind: kind, step: step, done: make(chan struct{}), lead: make(chan struct{}, 1), waiting: true}
	s.mu.Lock()
	s.queue = append(s.queue, job)
	leads := !s.draining
	s.draining = true
	s.mu.Unlock()
	if leads && s.drain(ctx, job) {
		return ctx.Err()
	}
	return s.await(ctx, job)
}

func (s *sink) dispatch(ctx context.Context, kind string, step func()) {
	if err := s.submit(ctx, kind, step); err != nil {
		s.log.Debug("sink waiter gave up, its step still runs in order", "step", kind, "err", err)
	}
}

func (s *sink) await(ctx context.Context, job *sinkJob) error {
	for {
		select {
		case <-job.done:
			return nil
		case <-job.lead:
			s.mu.Lock()
			job.promoted = false
			finished := job.finished
			s.mu.Unlock()
			if finished {
				return nil
			}
			if s.drain(ctx, job) {
				return ctx.Err()
			}
		case <-ctx.Done():
			if s.leave(job) {
				return nil
			}
			return ctx.Err()
		}
	}
}

func (s *sink) leave(job *sinkJob) bool {
	s.mu.Lock()
	job.waiting = false
	finished := job.finished
	pending := job.promoted && !finished
	job.promoted = false
	s.mu.Unlock()
	if pending {
		<-job.lead
		s.handOff()
	}
	return finished
}

func (s *sink) drain(ctx context.Context, own *sinkJob) bool {
	clean := false
	defer func() {
		if !clean {
			s.abort()
		}
	}()
	for range sinkDrainBudget {
		if ctx.Err() != nil {
			s.stepDown(own)
			clean = true
			return true
		}
		job := s.take()
		if job == nil {
			clean = true
			return false
		}
		s.execute(job)
	}
	s.handOff()
	clean = true
	return false
}

func (s *sink) stepDown(own *sinkJob) {
	s.mu.Lock()
	own.waiting = false
	s.mu.Unlock()
	s.handOff()
}

func (s *sink) take() *sinkJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		s.draining = false
		return nil
	}
	return s.pop()
}

func (s *sink) handOff() {
	clean := false
	defer func() {
		if !clean {
			s.abort()
		}
	}()
	for {
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.draining = false
			s.mu.Unlock()
			clean = true
			return
		}
		head := s.queue[0]
		if head.waiting {
			head.promoted = true
			head.lead <- struct{}{}
			s.mu.Unlock()
			clean = true
			return
		}
		s.pop()
		s.mu.Unlock()
		s.execute(head)
	}
}

func (s *sink) abort() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, job := range s.queue {
		s.log.Warn("sink step dropped after a panic", "step", job.kind)
		s.settleLocked(job)
	}
	s.queue = nil
	s.draining = false
}

func (s *sink) pop() *sinkJob {
	job := s.queue[0]
	s.queue[0] = nil
	s.queue = s.queue[1:]
	return job
}

func (s *sink) execute(job *sinkJob) {
	defer s.settle(job)
	if s.run().Err() != nil {
		s.log.Debug("sink step dropped, lifetime ended", "step", job.kind)
		return
	}
	job.step()
}

func (s *sink) settle(job *sinkJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settleLocked(job)
}

func (s *sink) settleLocked(job *sinkJob) {
	if job.finished {
		return
	}
	job.finished = true
	close(job.done)
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
	if err := s.rc.React(ctx, ref.chOr(s.ch), ref.id, ackEmoji); err != nil {
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
	if s.acked.id != "" {
		failed := s.acked
		s.clearAck(ctx, channel)
		if err := s.rc.React(ctx, channel, failed.id, failEmoji); err != nil {
			s.log.Warn("add failure reaction failed", "err", err)
		}
		s.acked = msgRef{}
	}
	if reason == "" {
		return
	}
	s.postParts(ctx, channel, s.answer(failEmoji+" "+reason))
}

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
	case "reply":
		s.onReply(ctx, event)
	}
}

func (s *sink) onHuman(ctx context.Context) {
	if level := s.level(); rendersProgress(level) {
		s.pv = newProgressView(s.upsertStatus, level, time.Now(), s.log)
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

func (s *sink) onReply(ctx context.Context, event contracts.Event) {
	if !event.Done {
		return
	}
	channel := s.ch
	s.postParts(ctx, channel, s.answer(event.Text))
	if s.pv != nil {
		if event.Cost > 0 {
			s.pv.add(contracts.BackendEvent{Kind: "result", Cost: event.Cost})
		}
		s.pv.finish()
		s.pv = nil
	}
	if s.tidyPing(ctx, channel) {
		return
	}
	s.clearAck(ctx, channel)
}

func (s *sink) postParts(ctx context.Context, channelID, text string) {
	for _, part := range chunkText(text, gatewayMaxLen) {
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

func (s *sink) answer(text string) string {
	if text == "" {
		text = "✅ terminé"
	}
	if s.owner == "" {
		return text
	}
	return "<@" + s.owner + "> " + text
}

func (s *sink) clearAck(ctx context.Context, channelID string) {
	if s.acked.id == "" {
		return
	}
	if err := s.rc.Unreact(ctx, s.acked.chOr(channelID), s.acked.id, ackEmoji); err != nil {
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
