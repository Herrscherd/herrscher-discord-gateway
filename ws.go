package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
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

const (
	closeAuthenticationFailed websocket.StatusCode = 4004
	closeInvalidShard         websocket.StatusCode = 4010
	closeShardingRequired     websocket.StatusCode = 4011
	closeInvalidAPIVersion    websocket.StatusCode = 4012
	closeInvalidIntents       websocket.StatusCode = 4013
	closeDisallowedIntents    websocket.StatusCode = 4014
)

const gatewayURL = "wss://gateway.discord.gg/?v=10&encoding=json"

const (
	intentGuilds         = 1 << 0
	intentGuildMessages  = 1 << 9
	intentDirectMessages = 1 << 12
)

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
	wsIntents           = intentGuilds | intentGuildMessages | intentDirectMessages
)

type ws struct {
	url       string
	token     string
	log       *slog.Logger
	handle    func(context.Context, dctl.Interaction)
	onMessage func(context.Context, messageCreate)
	onGone    func(context.Context, string)

	mu    sync.Mutex
	acked atomic.Bool
	seq   struct {
		sync.Mutex

		n int
	}
	sem     chan struct{}
	workers sync.WaitGroup
}

type gwPayload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  *int            `json:"s"`
	T  string          `json:"t"`
}

func newWS(token string, log *slog.Logger, handle func(context.Context, dctl.Interaction), onMessage func(context.Context, messageCreate), onGone func(context.Context, string)) *ws {
	return &ws{url: gatewayURL, token: token, log: log, handle: handle, onMessage: onMessage, onGone: onGone, sem: make(chan struct{}, maxInFlight)}
}

func (w *ws) run(ctx context.Context) {
	defer w.workers.Wait()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	backoff := time.Second
	for ctx.Err() == nil {
		connected := func() { backoff = time.Second }
		if err := w.session(ctx, connected); err != nil && ctx.Err() == nil {
			if code, ok := permanentClose(err); ok {
				w.log.Error("gateway closed permanently, not reconnecting, check the bot token and intents", "err", err, "close_code", int(code))
				return
			}
			w.log.Warn("gateway connection lost, reconnecting", "err", err, "backoff", backoff.String())
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
	return code, slices.Contains(permanentCloseCodes(), code)
}

func permanentCloseCodes() []websocket.StatusCode {
	return []websocket.StatusCode{
		closeAuthenticationFailed,
		closeInvalidShard,
		closeShardingRequired,
		closeInvalidAPIVersion,
		closeInvalidIntents,
		closeDisallowedIntents,
	}
}

func (w *ws) session(ctx context.Context, connected func()) error {
	conn, _, err := websocket.Dial(ctx, w.url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer w.closeSocket(conn, websocket.StatusNormalClosure, "")
	conn.SetReadLimit(readLimit)

	hello, err := readPayload(ctx, conn)
	if err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	if hello.Op != opHello {
		return fmt.Errorf("got op %d: %w", hello.Op, errNoHello)
	}
	var handshake struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	if err := json.Unmarshal(hello.D, &handshake); err != nil {
		return fmt.Errorf("hello payload: %w", err)
	}

	if err := w.identify(ctx, conn); err != nil {
		return fmt.Errorf("identify: %w", err)
	}
	connected()

	w.acked.Store(true)
	stopHeartbeat := w.startHeartbeat(ctx, conn, time.Duration(handshake.HeartbeatInterval)*time.Millisecond)
	defer stopHeartbeat()

	for {
		payload, err := readPayload(ctx, conn)
		if err != nil {
			return err
		}
		if payload.S != nil {
			w.setSeq(*payload.S)
		}
		switch payload.Op {
		case opDispatch:
			w.dispatch(ctx, payload.T, payload.D)
		case opHeartbeat:
			if err := w.writeHeartbeat(ctx, conn); err != nil {
				return err
			}
		case opReconnect, opInvalidSession:
			return fmt.Errorf("op %d: %w", payload.Op, errReconnectRequested)
		case opHeartbeatACK:
			w.acked.Store(true)
		}
	}
}

func (w *ws) dispatch(ctx context.Context, eventType string, data json.RawMessage) {
	switch eventType {
	case "INTERACTION_CREATE":
		if w.handle == nil {
			return
		}
		var interaction dctl.Interaction
		if err := json.Unmarshal(data, &interaction); err != nil {
			w.log.Warn("bad interaction payload dropped", "err", err)
			return
		}
		w.bounded(ctx, func() { w.handle(ctx, interaction) })
	case "MESSAGE_CREATE":
		if w.onMessage == nil {
			return
		}
		var message messageCreate
		if err := json.Unmarshal(data, &message); err != nil {
			w.log.Warn("bad message payload dropped", "err", err)
			return
		}
		w.bounded(ctx, func() { w.onMessage(ctx, message) })
	case "CHANNEL_DELETE", "THREAD_DELETE":
		if w.onGone == nil {
			return
		}
		var deletion channelDelete
		if err := json.Unmarshal(data, &deletion); err != nil {
			w.log.Warn("bad gone payload dropped", "event", eventType, "err", err)
			return
		}
		if deletion.ID == "" {
			return
		}
		w.bounded(ctx, func() { w.onGone(ctx, deletion.ID) })
	}
}

func (w *ws) bounded(ctx context.Context, work func()) {
	select {
	case w.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	w.workers.Add(1)
	go func() {
		defer w.workers.Done()
		defer func() { <-w.sem }()
		work()
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

func (w *ws) startHeartbeat(ctx context.Context, conn *websocket.Conn, interval time.Duration) func() {
	hbCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.heartbeatLoop(hbCtx, conn, interval)
	}()
	return func() {
		cancel()
		<-done
	}
}

func (w *ws) heartbeatLoop(ctx context.Context, conn *websocket.Conn, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.sendHeartbeat(ctx, conn); err != nil {
				w.closeSocket(conn, websocket.StatusInternalError, "heartbeat")
				return
			}
		}
	}
}

func (w *ws) sendHeartbeat(ctx context.Context, conn *websocket.Conn) error {
	if !w.acked.Swap(false) {
		return errHeartbeatUnacked
	}
	return w.writeHeartbeat(ctx, conn)
}

func (w *ws) writeHeartbeat(ctx context.Context, conn *websocket.Conn) error {
	w.seq.Lock()
	sequence := w.seq.n
	w.seq.Unlock()
	var seqPtr *int
	if sequence > 0 {
		seqPtr = &sequence
	}
	return w.write(ctx, conn, heartbeatMsg{Op: opHeartbeat, D: seqPtr})
}

func (w *ws) setSeq(sequence int) {
	w.seq.Lock()
	w.seq.n = sequence
	w.seq.Unlock()
}

func (w *ws) write(ctx context.Context, conn *websocket.Conn, v any) error {
	encoded, err := json.Marshal(v)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return conn.Write(ctx, websocket.MessageText, encoded)
}

func readPayload(ctx context.Context, c *websocket.Conn) (gwPayload, error) {
	_, data, err := c.Read(ctx)
	if err != nil {
		return gwPayload{}, err
	}
	var payload gwPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return gwPayload{}, fmt.Errorf("decode payload: %w", err)
	}
	return payload, nil
}

func (w *ws) closeSocket(c *websocket.Conn, code websocket.StatusCode, reason string) {
	if err := c.Close(code, reason); err != nil {
		w.log.Warn("close gateway socket failed", "err", err)
	}
}
