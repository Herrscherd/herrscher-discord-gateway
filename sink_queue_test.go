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

const sinkBubbleBound = 2 * time.Second

func runSinkBubble(t *testing.T, body func(*testing.T)) {
	t.Helper()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		synctest.Test(t, body)
	}()
	select {
	case <-finished:
	case <-time.After(sinkBubbleBound):
		t.Fatal("the sink bubble is still parked after its bound: a step holds the sink lock during I/O, or a waiter is parked on the mutex")
	}
}

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
	runSinkBubble(t, func(t *testing.T) {
		t.Helper()
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
	runSinkBubble(t, func(t *testing.T) {
		t.Helper()
		g := newGatedRender()
		s := newSink(testRun(), g, "c1", staticLevel(levelSilent), "", false, testLog())
		var wg sync.WaitGroup
		wg.Go(func() { s.fail(context.Background(), "a") })
		synctest.Wait()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		waiter := make(chan error, 1)
		go func() {
			waiter <- s.submit(ctx, "late", func() { g.gate("late") })
		}()
		select {
		case err := <-waiter:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("submit returned %v, want context.Canceled", err)
			}
		case <-time.After(sinkBubbleBound):
			t.Fatal("waiter still blocked behind a render call after its context ended")
		}

		close(g.release)
		wg.Wait()
		want := []string{"post:" + failEmoji + " a", "late"}
		if !slices.Equal(g.calls, want) {
			t.Fatalf("render calls = %q, want %q: a step whose waiter gave up must still run, in order", g.calls, want)
		}
	})
}

func TestSinkCancelledNoteDoesNotHideTheNextHumanAck(t *testing.T) {
	runSinkBubble(t, func(t *testing.T) {
		t.Helper()
		g := newGatedRender()
		s := newSink(testRun(), g, "c1", staticLevel(levelSilent), "", false, testLog())
		var wg sync.WaitGroup

		wg.Go(func() { s.fail(context.Background(), "a") })
		synctest.Wait()
		wg.Go(func() { s.noteUser(context.Background(), "c1", "m1", "42") })
		synctest.Wait()

		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		s.noteUser(cancelled, "c1", "m2", "42")

		close(g.release)
		wg.Wait()
		s.handle(contracts.Event{T: "human"})

		want := []string{"post:" + failEmoji + " a", "react:m2:" + ackEmoji}
		if !slices.Equal(g.calls, want) {
			t.Fatalf("render calls = %q, want %q: the human ack must target m2, the last message noted", g.calls, want)
		}
	})
}

func TestSinkCancelledAckStillReactsOnItsMessage(t *testing.T) {
	runSinkBubble(t, func(t *testing.T) {
		t.Helper()
		g := newGatedRender()
		s := newSink(testRun(), g, "c1", staticLevel(levelSilent), "", false, testLog())
		var wg sync.WaitGroup

		wg.Go(func() { s.fail(context.Background(), "a") })
		synctest.Wait()

		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		s.ack(cancelled, "c1", "m2", "42")

		close(g.release)
		wg.Wait()

		want := []string{"post:" + failEmoji + " a", "react:m2:" + ackEmoji}
		if !slices.Equal(g.calls, want) {
			t.Fatalf("render calls = %q, want %q: the abandoned ack must still mark m2", g.calls, want)
		}
	})
}

func TestSinkCancelledFailStillClosesTheOpenAck(t *testing.T) {
	runSinkBubble(t, func(t *testing.T) {
		t.Helper()
		g := newGatedRender()
		s := newSink(testRun(), g, "c1", staticLevel(levelSilent), "", false, testLog())
		var wg sync.WaitGroup

		wg.Go(func() { s.ack(context.Background(), "c1", "m1", "42") })
		synctest.Wait()

		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		s.fail(cancelled, "boom")

		close(g.release)
		wg.Wait()

		want := []string{
			"react:m1:" + ackEmoji,
			"unreact:m1:" + ackEmoji,
			"react:m1:" + failEmoji,
			"post:" + failEmoji + " boom",
		}
		if !slices.Equal(g.calls, want) {
			t.Fatalf("render calls = %q, want %q", g.calls, want)
		}
		if s.acked != (msgRef{}) || s.pv != nil {
			t.Fatalf("acked = %+v, pv open = %v: the failure must close both", s.acked, s.pv != nil)
		}
	})
}

func TestSinkHandsOverWhenItsContextEndsBetweenSteps(t *testing.T) {
	runSinkBubble(t, func(t *testing.T) {
		t.Helper()
		s := newSink(testRun(), &fakeRender{}, "c1", staticLevel(levelSilent), "", false, testLog())
		release := make(chan struct{})
		var order []int
		var leaderErr error
		var wg sync.WaitGroup

		leadCtx, cancelLead := context.WithCancel(context.Background())
		wg.Go(func() {
			leaderErr = s.submit(leadCtx, "lead", func() {
				<-release
				order = append(order, 0)
			})
		})
		synctest.Wait()
		wg.Go(func() {
			if err := s.submit(context.Background(), "next", func() { order = append(order, 1) }); err != nil {
				t.Errorf("waiter submit: %v", err)
			}
		})
		synctest.Wait()

		cancelLead()
		close(release)
		wg.Wait()

		if leaderErr != nil {
			t.Errorf("leader returned %v, want nil: its own step already ran", leaderErr)
		}
		if want := []int{0, 1}; !slices.Equal(order, want) {
			t.Fatalf("steps ran in order %v, want %v", order, want)
		}
	})
}

func TestSinkDropsQueuedWorkOnceTheLifetimeEnds(t *testing.T) {
	runSinkBubble(t, func(t *testing.T) {
		t.Helper()
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
	runSinkBubble(t, func(t *testing.T) {
		t.Helper()
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
	runSinkBubble(t, func(t *testing.T) {
		t.Helper()
		var logs bytes.Buffer
		log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		s := newSink(testRun(), &fakeRender{}, "c1", staticLevel(levelSilent), "", false, log)
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
		if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "step=queued") {
			t.Errorf("the discarded step was not logged at Warn with its kind: %s", logs.String())
		}

		if err := s.submit(context.Background(), "after", func() { afterRan = true }); err != nil {
			t.Fatalf("submit after the panic returned %v", err)
		}
		if !afterRan {
			t.Fatal("a submit after the panic did not run its step")
		}
	})
}

func TestSinkHandOverRunsTheStepOfAWaiterThatGaveUp(t *testing.T) {
	runSinkBubble(t, func(t *testing.T) {
		t.Helper()
		s := newSink(testRun(), &fakeRender{}, "c1", staticLevel(levelSilent), "", false, testLog())
		release := make(chan struct{})
		var order []int
		var leaderErr, gaveUpErr, lateErr error
		var wg sync.WaitGroup

		leadCtx, cancelLead := context.WithCancel(context.Background())
		wg.Go(func() {
			leaderErr = s.submit(leadCtx, "lead", func() {
				<-release
				order = append(order, 0)
			})
		})
		synctest.Wait()

		gaveUp, cancelGaveUp := context.WithCancel(context.Background())
		cancelGaveUp()
		gaveUpErr = s.submit(gaveUp, "gave-up", func() { order = append(order, 1) })

		cancelLead()
		close(release)
		wg.Wait()

		lateErr = s.submit(context.Background(), "late", func() { order = append(order, 2) })
		if !errors.Is(gaveUpErr, context.Canceled) {
			t.Errorf("waiter returned %v, want context.Canceled", gaveUpErr)
		}
		if leaderErr != nil {
			t.Errorf("leader returned %v, want nil: its own step already ran", leaderErr)
		}
		if lateErr != nil {
			t.Errorf("submit after the hand-over returned %v", lateErr)
		}
		if want := []int{0, 1, 2}; !slices.Equal(order, want) {
			t.Fatalf("steps ran in order %v, want %v: a waiter that gave up must not strand the drain", order, want)
		}
	})
}

func TestSinkPromotedWaiterThatGivesUpStillHandsOver(t *testing.T) {
	for range 20 {
		runSinkBubble(t, func(t *testing.T) {
			t.Helper()
			s := newSink(testRun(), &fakeRender{}, "c1", staticLevel(levelSilent), "", false, testLog())
			release := make(chan struct{})
			var order []int
			var wg sync.WaitGroup

			leadCtx, cancelLead := context.WithCancel(context.Background())
			waiterCtx, cancelWaiter := context.WithCancel(context.Background())
			wg.Go(func() {
				if err := s.submit(leadCtx, "lead", func() {
					<-release
					order = append(order, 0)
					cancelWaiter()
					cancelLead()
				}); err != nil {
					t.Errorf("lead submit: %v", err)
				}
			})
			synctest.Wait()
			wg.Go(func() {
				if err := s.submit(waiterCtx, "waiter", func() { order = append(order, 1) }); err != nil && !errors.Is(err, context.Canceled) {
					t.Errorf("waiter submit: %v", err)
				}
			})
			synctest.Wait()

			close(release)
			wg.Wait()

			if want := []int{0, 1}; !slices.Equal(order, want) {
				t.Fatalf("steps ran in order %v, want %v: a promoted waiter that gave up must still run its step", order, want)
			}
		})
	}
}

func TestSinkHandOffStopsAtItsBudgetAndLeavesTheTailForTheNextSubmit(t *testing.T) {
	runSinkBubble(t, func(t *testing.T) {
		t.Helper()
		s := newSink(testRun(), &fakeRender{}, "c1", staticLevel(levelSilent), "", false, testLog())
		release := make(chan struct{})
		var order []int
		var wg sync.WaitGroup

		leadCtx, cancelLead := context.WithCancel(context.Background())
		wg.Go(func() {
			if err := s.submit(leadCtx, "lead", func() {
				<-release
				order = append(order, 0)
			}); err != nil {
				t.Errorf("lead submit: %v", err)
			}
		})
		synctest.Wait()

		const abandoned = sinkDrainBudget + 4
		submitAbandoned(t, s, abandoned, &order)

		cancelLead()
		close(release)
		wg.Wait()
		if got := len(order); got != 1+sinkDrainBudget {
			t.Fatalf("steps run when the lead handed off = %d, want %d: handOff must honour its budget", got, 1+sinkDrainBudget)
		}

		if err := s.submit(context.Background(), "next", func() { order = append(order, 99) }); err != nil {
			t.Fatalf("next submit returned %v", err)
		}
		want := make([]int, 0, abandoned+2)
		for i := range abandoned + 1 {
			want = append(want, i)
		}
		want = append(want, 99)
		if !slices.Equal(order, want) {
			t.Fatalf("steps ran in order %v, want %v", order, want)
		}
	})
}

func submitAbandoned(t *testing.T, s *sink, n int, order *[]int) {
	t.Helper()
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 1; i <= n; i++ {
		step := i
		if err := s.submit(gone, "gone", func() { *order = append(*order, step) }); !errors.Is(err, context.Canceled) {
			t.Fatalf("abandoned submit returned %v, want context.Canceled", err)
		}
	}
}
