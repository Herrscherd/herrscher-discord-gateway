package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Herrscherd/dctl"
	"github.com/coder/websocket"
)

var (
	errNoHello            = errors.New("the gateway did not open with HELLO")
	errReconnectRequested = errors.New("the server asked to reconnect")
	errHeartbeatUnacked   = errors.New("heartbeat not acknowledged")
)

const gatewayURL = "wss://gateway.discord.gg/?v=10&encoding=json"

const (
	opDispatch          = 0
	opHeartbeat         = 1
	opIdentify          = 2
	opReconnect         = 7
	opInvalidSession    = 9
	opHello             = 10
	opHeartbeatACK      = 11
	readLimit           = 8 << 20
	maxReconnectBackoff = 30 * time.Second
	maxInFlight         = 64
	wsIntents           = 1 | (1 << 9) | (1 << 12)
)

type ws struct {
	token     string
	handle    func(context.Context, dctl.Interaction)
	onMessage func(context.Context, messageCreate)
	onGone    func(context.Context, string)

	mu    sync.Mutex
	acked atomic.Bool
	seq   struct {
		sync.Mutex
		n int
	}
	sem chan struct{}
}

type gwPayload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  *int            `json:"s"`
	T  string          `json:"t"`
}

func newWS(token string, handle func(context.Context, dctl.Interaction), onMessage func(context.Context, messageCreate), onGone func(context.Context, string)) *ws {
	return &ws{token: token, handle: handle, onMessage: onMessage, onGone: onGone, sem: make(chan struct{}, maxInFlight)}
}

func (w *ws) run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		connected := func() { backoff = time.Second }
		if err := w.session(ctx, connected); err != nil && ctx.Err() == nil {
			if code, ok := permanentClose(err); ok {
				fmt.Fprintf(os.Stderr, "discord gateway: fatal: %v (close %d); not reconnecting, check the bot token and intents\n", err, code)
				return
			}
			fmt.Fprintf(os.Stderr, "discord gateway: %v; reconnecting in %s\n", err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff *= 2; backoff > maxReconnectBackoff {
				backoff = maxReconnectBackoff
			}
		}
	}
}

func permanentClose(err error) (websocket.StatusCode, bool) {
	code := websocket.CloseStatus(err)
	switch code {
	case 4004, 4010, 4011, 4012, 4013, 4014:
		return code, true
	}
	return code, false
}

func (w *ws) session(ctx context.Context, connected func()) error {
	c, _, err := websocket.Dial(ctx, gatewayURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer closeSocket(c, websocket.StatusNormalClosure, "")
	c.SetReadLimit(readLimit)

	hello, err := readPayload(ctx, c)
	if err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	if hello.Op != opHello {
		return fmt.Errorf("got op %d: %w", hello.Op, errNoHello)
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
	connected()

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
			w.dispatch(ctx, p.T, p.D)
		case opHeartbeat:
			if err := w.writeHeartbeat(ctx, c); err != nil {
				return err
			}
		case opReconnect, opInvalidSession:
			return fmt.Errorf("op %d: %w", p.Op, errReconnectRequested)
		case opHeartbeatACK:
			w.acked.Store(true)
		}
	}
}

func (w *ws) dispatch(ctx context.Context, t string, d json.RawMessage) {
	switch t {
	case "INTERACTION_CREATE":
		if w.handle == nil {
			return
		}
		var ix dctl.Interaction
		if err := json.Unmarshal(d, &ix); err != nil {
			fmt.Fprintf(os.Stderr, "discord gateway: bad interaction: %v\n", err)
			return
		}
		w.bounded(ctx, func() { w.handle(ctx, ix) })
	case "MESSAGE_CREATE":
		if w.onMessage == nil {
			return
		}
		var m messageCreate
		if err := json.Unmarshal(d, &m); err != nil {
			fmt.Fprintf(os.Stderr, "discord gateway: bad message: %v\n", err)
			return
		}
		w.bounded(ctx, func() { w.onMessage(ctx, m) })
	case "CHANNEL_DELETE", "THREAD_DELETE":
		if w.onGone == nil {
			return
		}
		var c channelDelete
		if err := json.Unmarshal(d, &c); err != nil {
			fmt.Fprintf(os.Stderr, "discord gateway: bad %s: %v\n", t, err)
			return
		}
		if c.ID == "" {
			return
		}
		w.bounded(ctx, func() { w.onGone(ctx, c.ID) })
	}
}

func (w *ws) bounded(ctx context.Context, fn func()) {
	select {
	case w.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	go func() {
		defer func() { <-w.sem }()
		fn()
	}()
}

type identifyProps struct {
	OS      string `json:"os"`
	Browser string `json:"browser"`
	Device  string `json:"device"`
}

type identifyData struct {
	Token      string        `json:"token"`
	Intents    int           `json:"intents"`
	Properties identifyProps `json:"properties"`
}

type identifyMsg struct {
	Op int          `json:"op"`
	D  identifyData `json:"d"`
}

type heartbeatMsg struct {
	Op int  `json:"op"`
	D  *int `json:"d"`
}

func (w *ws) identify(ctx context.Context, c *websocket.Conn) error {
	return w.write(ctx, c, identifyMsg{
		Op: opIdentify,
		D: identifyData{
			Token:      w.token,
			Intents:    wsIntents,
			Properties: identifyProps{OS: "linux", Browser: "herrscher", Device: "herrscher"},
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
				closeSocket(c, websocket.StatusInternalError, "heartbeat")
				return
			}
		}
	}
}

func (w *ws) sendHeartbeat(ctx context.Context, c *websocket.Conn) error {
	if !w.acked.Swap(false) {
		return errHeartbeatUnacked
	}
	return w.writeHeartbeat(ctx, c)
}

func (w *ws) writeHeartbeat(ctx context.Context, c *websocket.Conn) error {
	w.seq.Lock()
	n := w.seq.n
	w.seq.Unlock()
	var d *int
	if n > 0 {
		d = &n
	}
	return w.write(ctx, c, heartbeatMsg{Op: opHeartbeat, D: d})
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

func closeSocket(c *websocket.Conn, code websocket.StatusCode, reason string) {
	if err := c.Close(code, reason); err != nil {
		warnf("close gateway socket: %v", err)
	}
}
