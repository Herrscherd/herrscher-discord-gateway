package discord

import (
	"context"
	"fmt"

	"github.com/Herrscherd/dctl"
	"github.com/Herrscherd/herrscher-contracts"
)

// client is the subset of *dctl.Client the adapter needs (injected for tests).
type client interface {
	Send(ctx context.Context, channelID, content string) (*dctl.Message, error)
	Reply(ctx context.Context, channelID, replyTo, content string) (*dctl.Message, error)
	React(ctx context.Context, channelID, messageID, emoji string) error
	// Unreact removes a reaction this bot added. It backs the contributed
	// `message unreact` verb; the rendering path reaches it through Platform.
	Unreact(ctx context.Context, channelID, messageID, emoji string) error
	SendSelectMenu(ctx context.Context, channelID, replyTo, content, customID string, options []dctl.SelectOption) (*dctl.Message, error)
	// ReadMessages backs the router's channel context. REST reads are not gated by
	// the message-content intent, which is how the bot sees what everyone said
	// without asking Discord for a privileged intent.
	ReadMessages(ctx context.Context, channelID string, limit int, after string) ([]dctl.Message, error)
	// GetMessage reads one message by id, for the one the ping replies to: it is
	// blanked like every other dispatch, and it is far past the window a channel
	// read covers — a reply target is whatever the operator scrolled back to.
	GetMessage(ctx context.Context, channelID, messageID string) (*dctl.Message, error)
	// CreatePrivateThread opens a thread nobody can see until they are added, and
	// AddThreadMember is what adds them. A private thread is created off the
	// channel rather than off a message, so the channel keeps no trace of it.
	CreatePrivateThread(ctx context.Context, channelID, name string) (string, error)
	AddThreadMember(ctx context.Context, threadID, userID string) error
	// StartThread opens a public thread hanging off a message. Everyone who reads
	// the channel sees it and its author is a member without being added, which
	// is what a support room wants — and it is the one kind of thread that does
	// not need the "create private threads" permission.
	StartThread(ctx context.Context, channelID, messageID, name string) (string, error)
	// EditMessage and DeleteMessage back the optional MessageEditor port. They
	// are on the narrow client interface like everything else here, so the tests
	// can drive them without a Discord.
	EditMessage(ctx context.Context, channelID, messageID, content string) (*dctl.Message, error)
	DeleteMessage(ctx context.Context, channelID, messageID string) error
}

var (
	_ contracts.Gateway                = (*Gateway)(nil)
	_ contracts.SessionControlReceiver = (*Gateway)(nil)
	_ contracts.EventSink              = (*Gateway)(nil)
	_ contracts.RoutedEventSink        = (*Gateway)(nil)
	_ contracts.MessageEditor          = (*Gateway)(nil)
)

// Gateway adapts the Discord REST client to contracts.Gateway. When built from
// real config it also carries a slash runtime and a rendering sink, so it drives
// the slash surface (SessionControlReceiver) and renders the live turn stream
// (EventSink).
type Gateway struct {
	c     client
	slash *slash
	sinks *sinks

	// deletes is the operator's opt-in for the `message delete` verb, and it
	// gates whether that verb is contributed at all. Everything an agent reads
	// out of a channel is text other people wrote, and delete is the only verb
	// it could ask for that nothing undoes — so the deployment where nobody
	// wants it does not merely refuse it, it never offers it.
	deletes bool
	// selfID is the bot's own user id, which is what makes "did this bot write
	// that message" answerable at all. Empty outside the real factory, and a
	// delete refuses rather than guesses when it is.
	selfID string
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
		// The host downloads the attachment urls this gateway hands it, and pins
		// them to what the gateway vouches for. Without this the allowlist is
		// empty and every screenshot is refused before it is fetched.
		AttachmentHosts: attachmentHosts,
	}
}

// EmitTo renders one live turn event into the conversation the host routed it
// to. It satisfies contracts.RoutedEventSink, which the host prefers over the
// flat EventSink — so each session renders into its own channel instead of one
// global default.
func (g *Gateway) EmitTo(conv contracts.Conversation, e contracts.Event) {
	if g.sinks == nil || conv.ID == "" {
		return
	}
	g.sinks.at(conv.ID).handle(e)
}

// Emit is the unrouted fallback for a host that does not route events. Without a
// conversation there is nothing to render into, so it drops rather than guessing
// a channel. It satisfies contracts.EventSink.
func (g *Gateway) Emit(contracts.Event) {}

func (g *Gateway) Post(ctx context.Context, conv contracts.Conversation, text string) (contracts.MessageID, error) {
	m, err := g.c.Send(ctx, conv.ID, text)
	if err != nil {
		return msgID(m), fmt.Errorf("discord post: %w", err)
	}
	return msgID(m), nil
}

func (g *Gateway) Reply(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, text string) (contracts.MessageID, error) {
	m, err := g.c.Reply(ctx, conv.ID, string(replyTo), text)
	if err != nil {
		return msgID(m), fmt.Errorf("discord reply: %w", err)
	}
	return msgID(m), nil
}

func (g *Gateway) React(ctx context.Context, conv contracts.Conversation, msg contracts.MessageID, emoji string) error {
	if err := g.c.React(ctx, conv.ID, string(msg), emoji); err != nil {
		return fmt.Errorf("discord react: %w", err)
	}
	return nil
}

func (g *Gateway) Menu(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, prompt string, opts []contracts.Choice) error {
	out := make([]dctl.SelectOption, 0, len(opts))
	for _, o := range opts {
		out = append(out, dctl.SelectOption{Label: o.Label, Value: o.Value})
	}
	// customID carries the conversation id so a click routes its component
	// interaction back to the originating conversation.
	if _, err := g.c.SendSelectMenu(ctx, conv.ID, string(replyTo), prompt, ChoiceCustomID(conv.ID), out); err != nil {
		return fmt.Errorf("discord menu: %w", err)
	}
	return nil
}

// Edit rewrites a message already sent. It satisfies contracts.MessageEditor.
func (g *Gateway) Edit(ctx context.Context, channelID, messageID, content string) error {
	if _, err := g.c.EditMessage(ctx, channelID, messageID, content); err != nil {
		return fmt.Errorf("discord edit: %w", err)
	}
	return nil
}

// Delete removes a message already sent. It satisfies contracts.MessageEditor.
func (g *Gateway) Delete(ctx context.Context, channelID, messageID string) error {
	if err := g.c.DeleteMessage(ctx, channelID, messageID); err != nil {
		return fmt.Errorf("discord delete: %w", err)
	}
	return nil
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

func (d discordClient) Unreact(ctx context.Context, channelID, messageID, emoji string) error {
	return d.c.Reactions().Remove(ctx, channelID, messageID, emoji)
}

func (d discordClient) SendSelectMenu(ctx context.Context, channelID, replyTo, content, customID string, options []dctl.SelectOption) (*dctl.Message, error) {
	return d.c.Components().SendSelectMenu(ctx, channelID, replyTo, content, customID, options)
}

func (d discordClient) ReadMessages(ctx context.Context, channelID string, limit int, after string) ([]dctl.Message, error) {
	return d.c.Messages().Read(ctx, channelID, limit, after)
}

func (d discordClient) GetMessage(ctx context.Context, channelID, messageID string) (*dctl.Message, error) {
	return d.c.Messages().Get(ctx, channelID, messageID)
}

func (d discordClient) CreatePrivateThread(ctx context.Context, channelID, name string) (string, error) {
	ch, err := d.c.Threads().StartPrivate(ctx, channelID, name)
	if err != nil {
		return "", err
	}
	if ch == nil {
		return "", nil
	}
	return ch.ID, nil
}

func (d discordClient) StartThread(ctx context.Context, channelID, messageID, name string) (string, error) {
	ch, err := d.c.Threads().Start(ctx, channelID, messageID, name)
	if err != nil {
		return "", err
	}
	if ch == nil {
		return "", nil
	}
	return ch.ID, nil
}

func (d discordClient) AddThreadMember(ctx context.Context, threadID, userID string) error {
	return d.c.Threads().AddMember(ctx, threadID, userID)
}

func (d discordClient) EditMessage(ctx context.Context, channelID, messageID, content string) (*dctl.Message, error) {
	return d.c.Messages().Edit(ctx, channelID, messageID, content)
}

func (d discordClient) DeleteMessage(ctx context.Context, channelID, messageID string) error {
	return d.c.Messages().Delete(ctx, channelID, messageID)
}

func msgID(m *dctl.Message) contracts.MessageID {
	if m == nil {
		return ""
	}
	return contracts.MessageID(m.ID)
}
