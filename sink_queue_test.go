package discord

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

type gatedRender struct {
	entered chan string
	release chan struct{}
	mu      sync.Mutex
	calls   []string
}

func newGatedRender() *gatedRender {
	return &gatedRender{entered: make(chan string, 16), release: make(chan struct{})}
}

func (g *gatedRender) gate(call string) {
	g.entered <- call
	<-g.release
	g.mu.Lock()
	g.calls = append(g.calls, call)
	g.mu.Unlock()
}

func (g *gatedRender) UpsertStatusMessage(_ context.Context, _, _, content string) (string, error) {
	g.gate("upsert:" + content)
	return "status1", nil
}

func (g *gatedRender) Post(_ context.Context, _, content string) error {
	g.gate("post:" + content)
	return nil
}

func (g *gatedRender) React(_ context.Context, _, id, emoji string) error {
	g.gate("react:" + id + ":" + emoji)
	return nil
}

func (g *gatedRender) Unreact(_ context.Context, _, id, emoji string) error {
	g.gate("unreact:" + id + ":" + emoji)
	return nil
}

func (g *gatedRender) Delete(_ context.Context, _, id string) error {
	g.gate("delete:" + id)
	return nil
}

func TestSinkCallsTheRenderClientOutsideItsLock(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newSink(testRun(), f, "c1", staticLevel(levelFull), "42", true, testLog())
	f.held = func() bool {
		if s.mu.TryLock() {
			s.mu.Unlock()
			return false
		}
		return true
	}

	s.noteUser("c1", "m1", "42")
	s.handle(contracts.Event{T: "human"})
	s.handle(contracts.Event{T: "status", Text: "Read x"})
	s.handle(contracts.Event{T: "reply", Text: "ok", Done: true, Cost: 0.1})
	s.noteUser("c1", "m2", "42")
	s.handle(contracts.Event{T: "human"})
	s.fail(context.Background(), "boom")

	if f.heldCalls != 0 {
		t.Fatalf("render client calls made while the sink lock was held = %d, want 0", f.heldCalls)
	}
	if len(f.upserts) == 0 || len(f.posts) == 0 || len(f.reacted) == 0 || len(f.unreacted) == 0 || len(f.deleted) == 0 {
		t.Fatalf("upserts=%d posts=%d reacted=%d unreacted=%d deleted=%d, want every render call exercised",
			len(f.upserts), len(f.posts), len(f.reacted), len(f.unreacted), len(f.deleted))
	}
}

func TestSinkKeepsConversationOrderUnderConcurrentEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := newGatedRender()
		s := newSink(testRun(), g, "c1", staticLevel(levelSilent), "", false, testLog())
		var wg sync.WaitGroup

		s.noteUser("c1", "m1", "42")
		wg.Go(func() { s.handle(contracts.Event{T: "human"}) })
		synctest.Wait()
		wg.Go(func() { s.ack(context.Background(), "c1", "m2", "42") })
		synctest.Wait()
		wg.Go(func() { s.fail(context.Background(), "boom") })
		synctest.Wait()
		wg.Go(func() { s.handle(contracts.Event{T: "reply", Text: "ok", Done: true}) })
		synctest.Wait()

		if got := len(g.entered); got != 1 {
			t.Errorf("render calls started before the first one returned = %d, want 1", got)
		}
		close(g.release)
		wg.Wait()

		want := []string{
			"react:m1:" + ackEmoji,
			"react:m2:" + ackEmoji,
			"unreact:m2:" + ackEmoji,
			"react:m2:" + failEmoji,
			"post:" + failEmoji + " boom",
			"post:ok",
		}
		if !slices.Equal(g.calls, want) {
			t.Fatalf("render calls = %q, want %q", g.calls, want)
		}
	})
}

func TestSinkDropsQueuedWorkOnceTheLifetimeEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		g := newGatedRender()
		s := newSink(lifetimeFrom(ctx), g, "c1", staticLevel(levelSilent), "", false, testLog())
		var wg sync.WaitGroup

		wg.Go(func() { s.fail(context.Background(), "a") })
		synctest.Wait()
		wg.Go(func() { s.fail(context.Background(), "b") })
		synctest.Wait()

		cancel()
		close(g.release)
		wg.Wait()
		s.fail(context.Background(), "c")

		want := []string{"post:" + failEmoji + " a"}
		if !slices.Equal(g.calls, want) {
			t.Fatalf("render calls = %q, want %q: work queued after the lifetime ended must not reach the client", g.calls, want)
		}
	})
}
