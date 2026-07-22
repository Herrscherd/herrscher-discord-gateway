package discord

import (
	"errors"
	"fmt"
	"testing"

	"github.com/coder/websocket"
)

// TestPermanentCloseStopsReconnect pins the behaviour that a non-recoverable
// gateway close (bad token → 4004, bad intents → 4013, …) is treated as fatal so
// the connect loop stops instead of respamming a doomed IDENTIFY every backoff.
func TestPermanentCloseStopsReconnect(t *testing.T) {
	authFail := websocket.CloseError{Code: 4004, Reason: "Authentication failed."}
	if code, ok := permanentClose(fmt.Errorf("read: %w", authFail)); !ok || code != 4004 {
		t.Fatalf("4004 (auth failed) must be permanent; got code=%d ok=%v", code, ok)
	}
	if _, ok := permanentClose(websocket.CloseError{Code: 4013}); !ok {
		t.Fatal("4013 (invalid intents) must be permanent")
	}
	// Transient failures must keep reconnecting.
	if _, ok := permanentClose(errors.New("dial: connection refused")); ok {
		t.Fatal("a plain (non-close) error must not be treated as permanent")
	}
	if _, ok := permanentClose(websocket.CloseError{Code: 1001}); ok {
		t.Fatal("a normal going-away close must not be treated as permanent")
	}
}
