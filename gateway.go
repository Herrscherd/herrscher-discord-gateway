package discord

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
	"github.com/Herrscherd/herrscher-contracts/i18n"
)

const gatewayKind = "discord"

type client interface {
	Send(ctx context.Context, channelID, content string) (*dctl.Message, error)
	Reply(ctx context.Context, channelID, replyTo, content string) (*dctl.Message, error)
	React(ctx context.Context, channelID, messageID, emoji string) error
	Unreact(ctx context.Context, channelID, messageID, emoji string) error
	SendSelectMenu(ctx context.Context, channelID, replyTo, content, customID string, options []dctl.SelectOption) (*dctl.Message, error)
	ReadMessages(ctx context.Context, channelID string, limit int, after string) ([]dctl.Message, error)
	GetMessage(ctx context.Context, channelID, messageID string) (*dctl.Message, error)
	CreatePrivateThread(ctx context.Context, channelID, name string) (string, error)
	AddThreadMember(ctx context.Context, threadID, userID string) error
	StartThread(ctx context.Context, channelID, messageID, name string) (string, error)
	EditMessage(ctx context.Context, channelID, messageID, content string) (*dctl.Message, error)
	DeleteMessage(ctx context.Context, channelID, messageID string) error
	DeleteChannel(ctx context.Context, channelID string) error
}

var (
	_ contracts.Gateway                = (*Gateway)(nil)
	_ contracts.SessionControlReceiver = (*Gateway)(nil)
	_ contracts.GatewayCloser          = (*Gateway)(nil)
	_ contracts.EventSink              = (*Gateway)(nil)
	_ contracts.RoutedEventSink        = (*Gateway)(nil)
	_ contracts.MessageEditor          = (*Gateway)(nil)
	_ contracts.NoticeSink             = (*Gateway)(nil)
)

// Gateway adapts the Discord REST client to contracts.Gateway. When built from
// real config it also carries a slash runtime and a rendering sink, so it drives
// the slash surface (SessionControlReceiver) and renders the live turn stream
// (EventSink).
type Gateway struct {
	c     client
	run   lifetime
	log   *slog.Logger
	slash *slash
	sinks *sinks
	msgs  messages
	langs sessionLanguages

	deletes bool
	selfID  string

	slashMu      sync.Mutex
	slashClosed  bool
	slashRunning *slashRuntime
}

// NewGateway returns a Gateway that sends through c, runs its slash surface on the
// lifetime run, renders its notices with msgs and logs to log. It starts no work: that begins when the host calls
// BindSessionControl.
func NewGateway(c client, run lifetime, msgs messages, log *slog.Logger) *Gateway {
	return &Gateway{c: c, run: run, msgs: msgs, log: log}
}

// BindSessionControl receives the daemon's runtime session controller and starts
// the slash command surface (sync + websocket loop). It satisfies
// contracts.SessionControlReceiver. Gateways built without a slash runtime (e.g.
// in tests) ignore the binding. Only the first binding starts the runtime and
// keeps its controller: a later binding, or one after Close, is ignored.
func (g *Gateway) BindSessionControl(ctrl contracts.SessionControl) {
	if g.slash == nil {
		return
	}
	g.slashMu.Lock()
	defer g.slashMu.Unlock()
	if g.slashClosed || g.slashRunning != nil {
		return
	}
	g.slash.ctrl = ctrl
	g.slashRunning = startSlashRuntime(g.run(), g.slash)
}

// Close stops the slash runtime started by BindSessionControl and waits for its
// goroutines to return, bounded by ctx. It returns nil when no runtime was started.
// Once closed, the gateway never starts a runtime again.
func (g *Gateway) Close(ctx context.Context) error {
	g.slashMu.Lock()
	g.slashClosed = true
	running := g.slashRunning
	g.slashMu.Unlock()
	if running == nil {
		return nil
	}
	return running.stop(ctx)
}

// Manifest describes the gateway to the host: its kind, its category, the
// capabilities Discord offers and the attachment hosts it accepts.
func (g *Gateway) Manifest() contracts.Manifest {
	return contracts.Manifest{
		Kind:            gatewayKind,
		Category:        contracts.CategoryGateway,
		Capabilities:    contracts.Capabilities{Reactions: true, SelectMenus: true, Replies: true},
		AttachmentHosts: attachmentHostList(),
	}
}

// EmitTo renders one live turn event into the conversation the host routed it
// to. It satisfies contracts.RoutedEventSink, which the host prefers over the
// flat EventSink, so each session renders into its own channel instead of one
// global default.
func (g *Gateway) EmitTo(conv contracts.Conversation, event contracts.Event) {
	if g.sinks == nil || conv.ID == "" {
		return
	}
	g.sinks.at(conv.ID).handle(event)
}

// Emit is the unrouted fallback for a host that does not route events. Without a
// conversation there is nothing to render into, so it drops rather than guessing
// a channel. It satisfies contracts.EventSink.
func (g *Gateway) Emit(contracts.Event) {}

// Post sends text as a new message in conv. It returns the id Discord gave the
// message, and an error that wraps the Discord failure.
func (g *Gateway) Post(ctx context.Context, conv contracts.Conversation, text string) (contracts.MessageID, error) {
	sent, err := g.c.Send(ctx, conv.ID, text)
	if err != nil {
		return msgID(sent), fmt.Errorf("discord post: %w", err)
	}
	return msgID(sent), nil
}

// Reply sends text as an answer to replyTo in conv. It returns the id Discord gave
// the answer, and an error that wraps the Discord failure.
func (g *Gateway) Reply(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, text string) (contracts.MessageID, error) {
	sent, err := g.c.Reply(ctx, conv.ID, string(replyTo), text)
	if err != nil {
		return msgID(sent), fmt.Errorf("discord reply: %w", err)
	}
	return msgID(sent), nil
}

// Notify renders notice in lang and posts it into conv. A zero lang renders in the
// operator's default language. It satisfies contracts.NoticeSink.
func (g *Gateway) Notify(ctx context.Context, conv contracts.Conversation, lang i18n.Language, notice contracts.Notice) (contracts.MessageID, error) {
	resolved := g.msgs.resolve(g.langs.pick(conv.ID, lang))
	text, renderErr := g.msgs.notice(resolved, notice)
	if renderErr != nil {
		text = g.msgs.renderFailed(resolved, notice, renderErr)
	}
	emoji := g.msgs.headIn(resolved, headFail)
	if renderErr == nil {
		emoji = g.msgs.headIn(resolved, noticeHead(notice.Code))
	}
	sent, err := g.Post(ctx, conv, withEmoji(emoji, text))
	if err != nil {
		return sent, err
	}
	if renderErr != nil {
		return sent, fmt.Errorf("discord notify: %w: %w", contracts.ErrNoticeFallbackPosted, renderErr)
	}
	return sent, nil
}

// React adds emoji to msg in conv. It returns an error that wraps the Discord
// failure.
func (g *Gateway) React(ctx context.Context, conv contracts.Conversation, msg contracts.MessageID, emoji string) error {
	if err := g.c.React(ctx, conv.ID, string(msg), emoji); err != nil {
		return fmt.Errorf("discord react: %w", err)
	}
	return nil
}

// Menu sends prompt in reply to replyTo with a select menu of opts. The menu carries
// the choice custom id of conv, so a pick is routed back to conv's session. It
// returns an error that wraps the Discord failure.
func (g *Gateway) Menu(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, prompt string, opts []contracts.Choice) error {
	out := make([]dctl.SelectOption, 0, len(opts))
	for _, o := range opts {
		out = append(out, dctl.SelectOption{Label: o.Label, Value: o.Value})
	}
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
	thread, err := d.c.Threads().StartPrivate(ctx, channelID, name)
	if err != nil {
		return "", err
	}
	if thread == nil {
		return "", nil
	}
	return thread.ID, nil
}

func (d discordClient) StartThread(ctx context.Context, channelID, messageID, name string) (string, error) {
	thread, err := d.c.Threads().Start(ctx, channelID, messageID, name)
	if err != nil {
		return "", err
	}
	if thread == nil {
		return "", nil
	}
	return thread.ID, nil
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

func (d discordClient) DeleteChannel(ctx context.Context, channelID string) error {
	return d.c.Channels().Delete(ctx, channelID)
}

func msgID(msg *dctl.Message) contracts.MessageID {
	if msg == nil {
		return ""
	}
	return contracts.MessageID(msg.ID)
}
