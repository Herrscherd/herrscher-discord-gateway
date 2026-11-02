package discord

import (
	"errors"
	"fmt"
	"testing"

	"github.com/coder/websocket"
)

func TestPermanentCloseStopsReconnect(t *testing.T) {
	authFail := websocket.CloseError{Code: 4004, Reason: "Authentication failed."}
	if code, ok := permanentClose(fmt.Errorf("read: %w", authFail)); !ok || code != 4004 {
		t.Fatalf("4004 (auth failed) must be permanent; got code=%d ok=%v", code, ok)
	}
	if _, ok := permanentClose(websocket.CloseError{Code: 4013}); !ok {
		t.Fatal("4013 (invalid intents) must be permanent")
	}
	if _, ok := permanentClose(errors.New("dial: connection refused")); ok {
		t.Fatal("a plain (non-close) error must not be treated as permanent")
	}
	if _, ok := permanentClose(websocket.CloseError{Code: 1001}); ok {
		t.Fatal("a normal going-away close must not be treated as permanent")
	}
}
