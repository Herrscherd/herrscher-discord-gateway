package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Herrscherd/dctl"
	"github.com/Herrscherd/herrscher-contracts"
	"github.com/coder/websocket"
)

const gatewayURL = "wss://gateway.discord.gg/?v=10&encoding=json"

// intentGuilds is the only intent we need (interactions don't require message intents).
const intentGuilds = 1 << 0

type gwPayload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  int             `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
}

// CommandSource maintains the bot's websocket connection (its online presence)
// and surfaces INTERACTION_CREATE events as contracts.InboundCommand. It records
// heartbeat ACKs into the injected Liveness (when non-nil) so the host's health
// reflects pure transport state.
type CommandSource struct {
	c     *dctl.Client
	token string
	appID string
	out   chan contracts.InboundCommand
	live  contracts.Liveness
}

// NewCommandSource builds a CommandSource for client c, authenticating the
// websocket IDENTIFY with token (the same bot token c was built with). appID is
// the bot's application id, needed by the per-command responder to edit deferred
// replies.
func NewCommandSource(c *dctl.Client, token, appID string) *CommandSource {
	return &CommandSource{c: c, token: token, appID: appID, out: make(chan contracts.InboundCommand, 16)}
}

// SetLiveness wires a transport-keepalive sink; the host calls this so its health
// learns of heartbeat ACKs.
func (s *CommandSource) SetLiveness(l contracts.Liveness) { s.live = l }

func (s *CommandSource) Commands() <-chan contracts.InboundCommand { return s.out }

// Run connects and processes events until ctx is cancelled or the connection
// drops. On connection loss it returns an error; the caller reconnects.
func (s *CommandSource) Run(ctx context.Context) error {
	if !s.c.Enabled() {
		return dctl.ErrDisabled
	}
	conn, _, err := websocket.Dial(ctx, gatewayURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(1 << 20)

	// First frame: Hello (op 10) with heartbeat_interval.
	var hello struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	first, err := readPayload(ctx, conn)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(first.D, &hello); err != nil {
		return err
	}

	// Identify (op 2).
	identify := map[string]any{
		"op": 2,
		"d": map[string]any{
			"token":      s.token,
			"intents":    intentGuilds,
			"properties": map[string]any{"os": "linux", "browser": "dctl", "device": "dctl"},
		},
	}
	if err := writeJSON(ctx, conn, identify); err != nil {
		return err
	}

	// Heartbeat loop. seq holds the last received sequence number, sent as the
	// heartbeat payload so Discord knows which events we've seen. beat lets the
	// event loop request an immediate heartbeat (op 1). A failed write closes the
	// connection so the blocked reader unblocks and Run returns to reconnect.
	var seq atomic.Int64
	beat := make(chan struct{}, 1)
	hbCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sendHeartbeat := func() error {
		var d any
		if n := seq.Load(); n != 0 {
			d = n
		}
		return writeJSON(hbCtx, conn, map[string]any{"op": 1, "d": d})
	}
	go func() {
		t := time.NewTicker(time.Duration(hello.HeartbeatInterval) * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
			case <-beat:
			}
			if err := sendHeartbeat(); err != nil {
				conn.Close(websocket.StatusInternalError, "heartbeat failed")
				return
			}
		}
	}()

	for {
		p, err := readPayload(ctx, conn)
		if err != nil {
			return err
		}
		if p.S != 0 {
			seq.Store(int64(p.S))
		}
		switch p.Op {
		case 1: // Server demands an immediate heartbeat.
			select {
			case beat <- struct{}{}:
			default:
			}
		case 7, 9: // Reconnect / Invalid Session: drop so the caller reconnects.
			return fmt.Errorf("gateway requested reconnect (op %d)", p.Op)
		case 11: // Heartbeat ACK
			if s.live != nil {
				s.live.HeartbeatAck(time.Now())
			}
		case 0:
			if p.T == "INTERACTION_CREATE" {
				var in dctl.Interaction
				if err := json.Unmarshal(p.D, &in); err == nil {
					select {
					case s.out <- s.mapInbound(in):
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}
		}
	}
}

// mapInbound converts a Discord interaction into the platform-neutral inbound
// command the host dispatches, attaching a per-command responder that owns the
// interaction id+token and the Discord reply mechanics.
func (s *CommandSource) mapInbound(in dctl.Interaction) contracts.InboundCommand {
	kind := contracts.KindCommand
	switch in.Type {
	case dctl.InteractionComponent:
		kind = contracts.KindChoicePick
	case dctl.InteractionAutocomplete:
		kind = contracts.KindSuggest
	}

	invoker := in.Member.User.ID

	var customID string
	if kind == contracts.KindChoicePick {
		// Resolve the menu's custom_id back to its neutral route (a session name);
		// the host only ever sees the route, never the wire encoding.
		if sess, ok := ParseChoiceCustomID(in.Data.CustomID); ok {
			customID = sess
		}
	}

	return contracts.InboundCommand{
		Kind: kind,
		Command: contracts.Command{
			Invoker: invoker,
			Data: contracts.CommandData{
				Name:     in.Data.Name,
				Options:  mapOptions(in.Data.Options),
				Values:   in.Data.Values,
				CustomID: customID,
			},
		},
		Responder: &responder{c: s.c, appID: s.appID, id: in.ID, token: in.Token},
	}
}

func mapOptions(opts []dctl.InteractionOption) []contracts.Option {
	if len(opts) == 0 {
		return nil
	}
	out := make([]contracts.Option, 0, len(opts))
	for _, o := range opts {
		out = append(out, contracts.Option{
			Name:    o.Name,
			Type:    contracts.OptionType(o.Type),
			Value:   o.Value,
			Focused: o.Focused,
			Options: mapOptions(o.Options),
		})
	}
	return out
}

func readPayload(ctx context.Context, conn *websocket.Conn) (gwPayload, error) {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return gwPayload{}, err
	}
	var p gwPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return gwPayload{}, fmt.Errorf("gateway decode: %w", err)
	}
	return p, nil
}

func writeJSON(ctx context.Context, conn *websocket.Conn, v any) error {
	buf, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, buf)
}
