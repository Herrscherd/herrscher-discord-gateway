package discord

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

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

func TestSinkRunsEventsInSubmissionOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := newGatedRender()
		s := newSink(testRun(), g, "c1", staticLevel(levelSilent), "", false, testLog())
		var wg sync.WaitGroup

		s.noteUser(context.Background(), "c1", "m1", "42")
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

type stamp struct{ producer, seq int }

func TestSinkKeepsEachProducersOrderUnderConcurrentSubmits(t *testing.T) {
	s := newSink(testRun(), &fakeRender{}, "c1", staticLevel(levelSilent), "", false, testLog())
	const producers, perProducer = 4, 50
	var order []stamp
	var wg sync.WaitGroup
	for p := range producers {
		wg.Go(func() {
			for i := range perProducer {
				if err := s.submit(context.Background(), "order", func() {
					order = append(order, stamp{producer: p, seq: i})
				}); err != nil {
					t.Errorf("submit: %v", err)
				}
			}
		})
	}
	wg.Wait()

	next := make([]int, producers)
	for _, got := range order {
		if got.seq != next[got.producer] {
			t.Fatalf("producer %d ran step %d, want step %d next", got.producer, got.seq, next[got.producer])
		}
		next[got.producer]++
	}
	if len(order) != producers*perProducer {
		t.Fatalf("steps run = %d, want %d", len(order), producers*perProducer)
	}
}

func TestSinkWaiterLeavesWhenItsContextEndsDuringARenderCall(t *testing.T) {
	g := newGatedRender()
	s := newSink(testRun(), g, "c1", staticLevel(levelSilent), "", false, testLog())
	leader := make(chan struct{})
	go func() {
		defer close(leader)
		s.fail(context.Background(), "a")
	}()
	<-g.entered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var lateRan bool
	waiter := make(chan error, 1)
	go func() {
		waiter <- s.submit(ctx, "late", func() { lateRan = true })
	}()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("submit returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter still blocked behind a render call after its context ended")
	}

	close(g.release)
	<-leader
	if lateRan {
		t.Fatal("a step whose waiter gave up still ran")
	}
	want := []string{"post:" + failEmoji + " a"}
	if !slices.Equal(g.calls, want) {
		t.Fatalf("render calls = %q, want %q", g.calls, want)
	}
}

func TestSinkDropsQueuedWorkOnceTheLifetimeEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		g := newGatedRender()
		var logs bytes.Buffer
		log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		s := newSink(lifetimeFrom(ctx), g, "c1", staticLevel(levelSilent), "", false, log)
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
		if dropped := strings.Count(logs.String(), "step=fail"); dropped != 2 {
			t.Fatalf("dropped steps logged = %d, want 2 (b and c): %s", dropped, logs.String())
		}
	})
}

func budgetStep(i int, release, hold <-chan struct{}, order *[]int) func() {
	return func() {
		switch i {
		case 0:
			<-release
		case sinkDrainBudget:
			<-hold
		}
		*order = append(*order, i)
	}
}

func TestSinkHandsTheDrainOverAfterItsBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newSink(testRun(), &fakeRender{}, "c1", staticLevel(levelSilent), "", false, testLog())
		release := make(chan struct{})
		hold := make(chan struct{})
		const jobs = sinkDrainBudget + 4
		var order []int
		var wg sync.WaitGroup
		leaderDone := make(chan struct{})

		wg.Go(func() {
			s.dispatch(context.Background(), "job", budgetStep(0, release, hold, &order))
			close(leaderDone)
		})
		synctest.Wait()
		for i := 1; i < jobs; i++ {
			wg.Go(func() { s.dispatch(context.Background(), "job", budgetStep(i, release, hold, &order)) })
			synctest.Wait()
		}

		close(release)
		<-leaderDone
		close(hold)
		wg.Wait()

		want := make([]int, jobs)
		for i := range want {
			want[i] = i
		}
		if !slices.Equal(order, want) {
			t.Fatalf("steps ran in order %v, want %v", order, want)
		}
	})
}

func TestSinkRecoversFromAPanickingStep(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newSink(testRun(), &fakeRender{}, "c1", staticLevel(levelSilent), "", false, testLog())
		release := make(chan struct{})
		var recovered any
		var queuedRan, afterRan bool
		var queuedErr error
		var wg sync.WaitGroup

		wg.Go(func() {
			defer func() { recovered = recover() }()
			s.dispatch(context.Background(), "panic", func() {
				<-release
				panic("step failed")
			})
		})
		synctest.Wait()
		wg.Go(func() {
			queuedErr = s.submit(context.Background(), "queued", func() { queuedRan = true })
		})
		synctest.Wait()

		close(release)
		wg.Wait()
		if recovered != "step failed" {
			t.Errorf("recovered = %v, want the step's panic", recovered)
		}
		if queuedRan || queuedErr != nil {
			t.Errorf("queued submit ran = %v, err = %v; want it released without running", queuedRan, queuedErr)
		}

		if err := s.submit(context.Background(), "after", func() { afterRan = true }); err != nil {
			t.Fatalf("submit after the panic returned %v", err)
		}
		if !afterRan {
			t.Fatal("a submit after the panic did not run its step")
		}
	})
}
