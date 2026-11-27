package discord

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
	"github.com/Herrscherd/herrscher-contracts/i18n"
)

const gatewayKind = "discord"

type client interface {
	Send(ctx context.Context, channelID, content string) (*dctl.Message, error)
	Reply(ctx context.Context, channelID, replyTo, content string) (*dctl.Message, error)
	SendFiles(ctx context.Context, channelID, content string, files []dctl.File) (*dctl.Message, error)
	ReplyFiles(ctx context.Context, channelID, replyTo, content string, files []dctl.File) (*dctl.Message, error)
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
	_ contracts.FileSender             = (*Gateway)(nil)
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

	deletes    bool
	selfID     string
	fileLimit  int64
	batchLimit int64

	voices *voiceBots
	wsURL  string
	newBot func(token string) *dctl.Client

	slashMu      sync.Mutex
	bound        bool
	slashClosed  bool
	slashRunning *slashRuntime
}

// NewGateway returns a Gateway that sends through c, runs its slash surface on the
// lifetime run, renders its notices with msgs and logs to log. It starts no work: that begins when the host calls
// BindSessionControl.
func NewGateway(c client, run lifetime, msgs messages, log *slog.Logger) *Gateway {
	return &Gateway{c: c, run: run, msgs: msgs, log: log, fileLimit: maxFileBytes, batchLimit: maxBatchBytes}
}

// BindSessionControl receives the daemon's runtime session controller and starts
// the slash command surface (sync + websocket loop). It satisfies
// contracts.SessionControlReceiver. Gateways built without a slash runtime (e.g.
// in tests) ignore the binding. Only the first binding starts the runtime and
// keeps its controller: a later binding, or one after Close, is ignored. The
// house bot starts at once; the agent bots are identified inside the same
// runtime, each under its own timeout, so a Close cancels them and never waits
// on Discord.
func (g *Gateway) BindSessionControl(ctrl contracts.SessionControl) {
	if g.slash == nil {
		return
	}
	g.slashMu.Lock()
	if g.slashClosed || g.bound {
		g.slashMu.Unlock()
		return
	}
	g.bound = true
	g.slashMu.Unlock()

	voices := directoryVoices(ctrl)
	g.slash.ctrl = ctrl
	g.slash.attach = func(ctx context.Context) { g.attachAgents(ctx, voices) }
	contentHouse := houseNeedsContent(voices, tokenGiven)
	if contentHouse {
		g.slash.socket.intents |= intentMessageContent
	}
	if g.slash.router != nil {
		g.slash.router.setHouseContent(contentHouse)
	}

	g.slashMu.Lock()
	defer g.slashMu.Unlock()
	if g.slashClosed {
		return
	}
	g.expectAgents(voices)
	g.slashRunning = startSlashRuntime(g.run(), g.slash)
}

func (g *Gateway) expectAgents(voices []contracts.AgentVoice) {
	var agents []string
	for _, voice := range agentBotVoices(voices) {
		agents = append(agents, voice.Agent)
	}
	g.voices.expect(agents)
}

func agentBotVoices(voices []contracts.AgentVoice) []contracts.AgentVoice {
	var bots []contracts.AgentVoice
	for _, voice := range voices {
		if tokenGiven(voice) && validAgent(voice.Agent) {
			bots = append(bots, voice)
		}
	}
	return bots
}

func tokenGiven(voice contracts.AgentVoice) bool {
	return voice.Token.Valid()
}

func (g *Gateway) ownsBot(voice contracts.AgentVoice) bool {
	_, ok := g.voices.ownOf(voice.Agent)
	return ok
}

func houseNeedsContent(voices []contracts.AgentVoice, owned func(contracts.AgentVoice) bool) bool {
	for _, voice := range voices {
		if validAgent(voice.Agent) && voice.Reply == contracts.ReplyInChannels && !owned(voice) {
			return true
		}
	}
	return false
}

const appIDTimeout = 10 * time.Second

type resolvedAgent struct {
	voice contracts.AgentVoice
	bot   *dctl.Client
	appID string
}

func (g *Gateway) attachAgents(ctx context.Context, voices []contracts.AgentVoice) {
	sockets := g.bindAgents(ctx, voices)
	var running sync.WaitGroup
	for _, socket := range sockets {
		running.Add(1)
		go func() {
			defer running.Done()
			socket.run(ctx)
		}()
	}
	running.Wait()
}

func (g *Gateway) bindAgents(ctx context.Context, voices []contracts.AgentVoice) []*ws {
	if g.voices == nil {
		return nil
	}
	resolved := g.resolveAgents(ctx, voices)
	g.settleHouseContent(voices)
	sockets := make([]*ws, 0, len(resolved))
	for _, agent := range resolved {
		sockets = append(sockets, g.agentSocket(agent))
	}
	return sockets
}

func (g *Gateway) settleHouseContent(voices []contracts.AgentVoice) {
	if g.slash == nil {
		return
	}
	content := houseNeedsContent(voices, g.ownsBot)
	if content && g.slash.socket != nil {
		g.slash.socket.require(intentMessageContent)
	}
	if g.slash.router != nil {
		g.slash.router.setHouseContent(content)
	}
}

func (g *Gateway) agentSocket(agent resolvedAgent) *ws {
	name := agent.voice.Agent
	content := agent.voice.Reply == contracts.ReplyInChannels
	onMessage := func(ctx context.Context, msg messageCreate) {
		g.slash.router.onAgentMessage(ctx, name, msg, content)
	}
	socket := newWS(g.socketURL(), agent.voice.Token.Secret(), g.log, nil, onMessage, nil)
	if content {
		socket.intents |= intentMessageContent
	}
	return socket
}

func (g *Gateway) resolveAgents(ctx context.Context, voices []contracts.AgentVoice) []resolvedAgent {
	g.warnInvalidAgentNames(voices)
	candidates := agentBotVoices(voices)
	results := make([]*resolvedAgent, len(candidates))
	var pending sync.WaitGroup
	for index, voice := range candidates {
		pending.Add(1)
		go func() {
			defer pending.Done()
			results[index] = g.resolveAgent(ctx, voice)
		}()
	}
	pending.Wait()
	var resolved []resolvedAgent
	for _, result := range results {
		if result != nil {
			resolved = append(resolved, *result)
		}
	}
	return resolved
}

func (g *Gateway) warnInvalidAgentNames(voices []contracts.AgentVoice) {
	for _, voice := range voices {
		if tokenGiven(voice) && !validAgent(voice.Agent) {
			g.log.Warn("agent name is not a valid bot name, the agent gets no bot of its own", "agent", voice.Agent)
		}
	}
}

func (g *Gateway) resolveAgent(ctx context.Context, voice contracts.AgentVoice) *resolvedAgent {
	defer g.voices.settle(voice.Agent)
	bot := g.botFor(voice.Token.Secret())
	appID, err := appIDOf(ctx, bot)
	if err != nil {
		if ctx.Err() == nil {
			g.log.Warn("agent bot token rejected, the agent answers through the house bot", "agent", voice.Agent, "err", err)
		}
		return nil
	}
	g.voices.register(voice.Agent, bot, appID)
	return &resolvedAgent{voice: voice, bot: bot, appID: appID}
}

func appIDOf(ctx context.Context, bot *dctl.Client) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, appIDTimeout)
	defer cancel()
	return bot.Interactions().AppID(ctx)
}

func (g *Gateway) botFor(token string) *dctl.Client {
	if g.newBot != nil {
		return g.newBot(token)
	}
	return dctl.New(token, "")
}

func (g *Gateway) socketURL() string {
	if g.wsURL != "" {
		return g.wsURL
	}
	return gatewayURL
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

// Post sends text as new messages in conv, split so that each part fits Discord's
// limit with the agent header of a voiced conversation. It returns the id of the
// first message sent, and an error that wraps the Discord failure.
func (g *Gateway) Post(ctx context.Context, conv contracts.Conversation, text string) (contracts.MessageID, error) {
	sent, err := g.sendParts(ctx, conv, "", splitText(text, g.voices.textLimit(conv.ID)))
	if err != nil {
		return sent, fmt.Errorf("discord post: %w", err)
	}
	return sent, nil
}

// Reply sends text as answers to replyTo in conv, split like Post. It returns the
// id of the first answer sent, and an error that wraps the Discord failure.
func (g *Gateway) Reply(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, text string) (contracts.MessageID, error) {
	sent, err := g.sendParts(ctx, conv, replyTo, splitText(text, g.voices.textLimit(conv.ID)))
	if err != nil {
		return sent, fmt.Errorf("discord reply: %w", err)
	}
	return sent, nil
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
	sent, err := g.sendParts(ctx, conv, "", chunkText(withEmoji(emoji, text), g.voices.textLimit(conv.ID)))
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
	parts := splitText(prompt, g.voices.menuLimit(conv.ID))
	lead, last := parts[:len(parts)-1], parts[len(parts)-1]
	replyTo, err := g.sendLeadParts(ctx, conv, replyTo, lead)
	if err != nil {
		return fmt.Errorf("discord menu: %w", err)
	}
	if _, err := g.c.SendSelectMenu(ctx, conv.ID, string(replyTo), last, ChoiceCustomID(conv.ID), out); err != nil {
		return fmt.Errorf("discord menu: %w", err)
	}
	return nil
}

func (g *Gateway) sendLeadParts(ctx context.Context, conv contracts.Conversation, replyTo contracts.MessageID, parts []string) (contracts.MessageID, error) {
	for _, part := range parts {
		if _, err := g.sendText(ctx, conv, replyTo, part); err != nil {
			return replyTo, err
		}
		replyTo = ""
	}
	return replyTo, nil
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

func (d discordClient) SendFiles(ctx context.Context, channelID, content string, files []dctl.File) (*dctl.Message, error) {
	return d.c.Messages().SendFiles(ctx, channelID, content, files)
}

func (d discordClient) ReplyFiles(ctx context.Context, channelID, replyTo, content string, files []dctl.File) (*dctl.Message, error) {
	return d.c.Messages().ReplyFiles(ctx, channelID, replyTo, content, files)
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
