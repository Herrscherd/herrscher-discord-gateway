package discord

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func runUntilStopped(t *testing.T, w *ws) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.run(ctx)
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("run did not return after its context was cancelled")
		}
	}
}

func TestRequiringAnIntentReidentifiesWithoutTheReconnectBackoff(t *testing.T) {
	identify := make(chan identified, 8)
	w := newWS(identifyingGateway(t, identify), "t", testLog(), nil, nil, nil)
	w.reconnectBackoff = time.Minute
	stop := runUntilStopped(t, w)
	defer stop()
	waitForIdentify(t, identify, func(identified) bool { return true }, "the first connection")

	w.require(intentMessageContent)

	select {
	case got := <-identify:
		if got.intents&intentMessageContent == 0 {
			t.Fatalf("re-identified with intents %d, want the content intent added", got.intents)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the restart for the content intent waited for the reconnect backoff")
	}
}

func TestRestartingForAnIntentDoesNotWarnAboutTheClosedSocket(t *testing.T) {
	identify := make(chan identified, 8)
	var logs lockedBuffer
	w := newWS(identifyingGateway(t, identify), "t", slog.New(slog.NewTextHandler(&logs, nil)), nil, nil, nil)
	stop := runUntilStopped(t, w)
	waitForIdentify(t, identify, func(identified) bool { return true }, "the first connection")

	w.require(intentMessageContent)
	waitForIdentify(t, identify, func(identified) bool { return true }, "the restarted connection")
	stop()

	if strings.Contains(logs.String(), "close gateway socket failed") {
		t.Fatalf("restarting for an intent logged a warning about the socket it had already closed:\n%s", logs.String())
	}
}
