package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Herrscherd/dctl"
	"github.com/coder/websocket"
)

// gatewayURL is the Discord Gateway v10 endpoint (JSON encoding). Interactions
// are delivered over the gateway regardless of intents, so we identify with
// intents=0 and only ever read INTERACTION_CREATE dispatches.
const gatewayURL = "wss://gateway.discord.gg/?v=10&encoding=json"

// Gateway opcodes (v10) we handle.
const (
	opDispatch          = 0
	opHeartbeat         = 1
	opIdentify          = 2
	opReconnect         = 7
	opInvalidSession    = 9
	opHello             = 10
	opHeartbeatACK      = 11
	readLimit           = 1 << 20
	maxReconnectBackoff = 30 * time.Second
)

// ws is the Discord Gateway websocket client. It connects, identifies, keeps the
// connection alive with heartbeats, and forwards every INTERACTION_CREATE to
// handle. It reconnects with exponential backoff until its context is cancelled.
type ws struct {
	token  string
	handle func(context.Context, dctl.Interaction)

	mu    sync.Mutex // serializes writes (only one Write may be in flight)
	acked atomic.Bool
	seq   struct {
		sync.Mutex
		n int
	}
}

type gwPayload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  *int            `json:"s"`
	T  string          `json:"t"`
}

func newWS(token string, handle func(context.Context, dctl.Interaction)) *ws {
	return &ws{token: token, handle: handle}
}

// run is the supervised connect loop: it keeps a session up, reconnecting with
// exponential backoff, until ctx is cancelled.
func (w *ws) run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := w.session(ctx); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "discord gateway: %v; reconnecting in %s\n", err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff *= 2; backoff > maxReconnectBackoff {
				backoff = maxReconnectBackoff
			}
			continue
		}
		backoff = time.Second
	}
}

// session runs one connection: dial, HELLO, IDENTIFY, then read until error.
func (w *ws) session(ctx context.Context) error {
	c, _, err := websocket.Dial(ctx, gatewayURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	c.SetReadLimit(readLimit)

	hello, err := readPayload(ctx, c)
	if err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	if hello.Op != opHello {
		return fmt.Errorf("expected HELLO, got op %d", hello.Op)
	}
	var h struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	if err := json.Unmarshal(hello.D, &h); err != nil {
		return fmt.Errorf("hello payload: %w", err)
	}

	if err := w.identify(ctx, c); err != nil {
		return fmt.Errorf("identify: %w", err)
	}

	// A fresh connection starts "acked" so the first heartbeat is allowed; each
	// heartbeat then requires the previous one to have been ACKed (see
	// sendHeartbeat), which is how a half-dead connection is detected.
	w.acked.Store(true)
	hbCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go w.heartbeatLoop(hbCtx, c, time.Duration(h.HeartbeatInterval)*time.Millisecond)

	for {
		p, err := readPayload(ctx, c)
		if err != nil {
			return err
		}
		if p.S != nil {
			w.setSeq(*p.S)
		}
		switch p.Op {
		case opDispatch:
			if p.T == "INTERACTION_CREATE" {
				w.onDispatch(ctx, p.D)
			}
		case opHeartbeat:
			// An explicit server request to beat now; honor it without the
			// missed-ACK check (that gate belongs to the periodic loop).
			if err := w.writeHeartbeat(ctx, c); err != nil {
				return err
			}
		case opReconnect, opInvalidSession:
			return fmt.Errorf("server asked to reconnect (op %d)", p.Op)
		case opHeartbeatACK:
			w.acked.Store(true)
		}
	}
}

func (w *ws) onDispatch(ctx context.Context, d json.RawMessage) {
	var ix dctl.Interaction
	if err := json.Unmarshal(d, &ix); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: bad interaction: %v\n", err)
		return
	}
	go w.handle(ctx, ix)
}

func (w *ws) identify(ctx context.Context, c *websocket.Conn) error {
	return w.write(ctx, c, map[string]any{
		"op": opIdentify,
		"d": map[string]any{
			"token":   w.token,
			"intents": 0,
			"properties": map[string]any{
				"os":      "linux",
				"browser": "herrscher",
				"device":  "herrscher",
			},
		},
	})
}

func (w *ws) heartbeatLoop(ctx context.Context, c *websocket.Conn, interval time.Duration) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.sendHeartbeat(ctx, c); err != nil {
				// Close so the blocked read loop unblocks and session() returns,
				// triggering a reconnect (a zombie connection is otherwise invisible).
				c.Close(websocket.StatusInternalError, "heartbeat")
				return
			}
		}
	}
}

// sendHeartbeat is the periodic beat: it first checks the previous beat was
// ACKed (else the connection is a zombie and it errors to force a reconnect).
func (w *ws) sendHeartbeat(ctx context.Context, c *websocket.Conn) error {
	if !w.acked.Swap(false) {
		return fmt.Errorf("heartbeat not acknowledged")
	}
	return w.writeHeartbeat(ctx, c)
}

func (w *ws) writeHeartbeat(ctx context.Context, c *websocket.Conn) error {
	w.seq.Lock()
	n := w.seq.n
	w.seq.Unlock()
	var d any
	if n > 0 {
		d = n
	}
	return w.write(ctx, c, map[string]any{"op": opHeartbeat, "d": d})
}

func (w *ws) setSeq(n int) {
	w.seq.Lock()
	w.seq.n = n
	w.seq.Unlock()
}

func (w *ws) write(ctx context.Context, c *websocket.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return c.Write(ctx, websocket.MessageText, b)
}

func readPayload(ctx context.Context, c *websocket.Conn) (gwPayload, error) {
	_, data, err := c.Read(ctx)
	if err != nil {
		return gwPayload{}, err
	}
	var p gwPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return gwPayload{}, fmt.Errorf("decode payload: %w", err)
	}
	return p, nil
}
