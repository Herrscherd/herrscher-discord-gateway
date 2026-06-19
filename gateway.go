package discord

import (
	"context"

	"github.com/Herrscherd/dctl"
	"github.com/Herrscherd/herrscher-contracts"
)

// client is the subset of *dctl.Client the adapter needs (injected for tests).
type client interface {
	Send(ctx context.Context, channelID, content string) (*dctl.Message, error)
	Reply(ctx context.Context, channelID, replyTo, content string) (*dctl.Message, error)
	React(ctx context.Context, channelID, messageID, emoji string) error
	SendSelectMenu(ctx context.Context, channelID, replyTo, content, customID string, options []dctl.SelectOption) (*dctl.Message, error)
}

var (
	_ contracts.Gateway                = (*Gateway)(nil)
	_ contracts.SessionControlReceiver = (*Gateway)(nil)
)

// Gateway adapts the Discord REST client to contracts.Gateway. When built from
// real config it also carries a slash runtime, so it drives the Discord slash
// command surface and implements contracts.SessionControlReceiver.
type Gateway struct {
	c     client
	slash *slash
}

func NewGateway(c client) *Gateway { return &Gateway{c: c} }

// BindSessionControl receives the daemon's runtime session controller and starts
// the slash command surface (sync + websocket loop). It satisfies
// contracts.SessionControlReceiver. Gateways built without a slash runtime (e.g.
// in tests) ignore the binding.
func (g *Gateway) BindSessionControl(ctrl contracts.SessionControl) {
	if g.slash == nil {
		return
	}
	g.slash.ctrl = ctrl
	go g.slash.start()
}

func (g *Gateway) Manifest() contracts.Manifest {
	return contracts.Manifest{
		Kind:         "discord",
		Category:     contracts.CategoryGateway,
		Capabilities: contracts.Capabilities{Reactions: true, SelectMenus: true, Replies: true},
	}
}

func (g *Gateway) Post(ctx context.Context, conv contracts.Conversation, text string) (contracts.MessageID, error) {
	m, err := g.c.Send(ctx, conv.ID, text)
	return msgID(m), err
}

func (g *Gateway) Reply(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, text string) (contracts.MessageID, error) {
	m, err := g.c.Reply(ctx, conv.ID, string(replyTo), text)
	return msgID(m), err
}

func (g *Gateway) React(ctx context.Context, conv contracts.Conversation, msg contracts.MessageID, emoji string) error {
	return g.c.React(ctx, conv.ID, string(msg), emoji)
}

func (g *Gateway) Menu(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, prompt string, opts []contracts.Choice) error {
	out := make([]dctl.SelectOption, 0, len(opts))
	for _, o := range opts {
		out = append(out, dctl.SelectOption{Label: o.Label, Value: o.Value})
	}
	// customID carries the conversation id so a click routes its component
	// interaction back to the originating conversation.
	_, err := g.c.SendSelectMenu(ctx, conv.ID, string(replyTo), prompt, ChoiceCustomID(conv.ID), out)
	return err
}

// discordClient adapts *dctl.Client's sub-clients to the narrow client seam the
// Gateway needs (and that tests fake).
type discordClient struct{ c *dctl.Client }

func (d discordClient) Send(ctx context.Context, channelID, content string) (*dctl.Message, error) {
	return d.c.Messages().Send(ctx, channelID, content)
}

func (d discordClient) Reply(ctx context.Context, channelID, replyTo, content string) (*dctl.Message, error) {
	return d.c.Messages().Reply(ctx, channelID, replyTo, content)
}

func (d discordClient) React(ctx context.Context, channelID, messageID, emoji string) error {
	return d.c.Reactions().Add(ctx, channelID, messageID, emoji)
}

func (d discordClient) SendSelectMenu(ctx context.Context, channelID, replyTo, content, customID string, options []dctl.SelectOption) (*dctl.Message, error) {
	return d.c.Components().SendSelectMenu(ctx, channelID, replyTo, content, customID, options)
}

func msgID(m *dctl.Message) contracts.MessageID {
	if m == nil {
		return ""
	}
	return contracts.MessageID(m.ID)
}
