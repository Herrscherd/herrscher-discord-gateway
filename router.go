package discord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

type routerConfig struct {
	owner           string
	appID           string
	voices          *voiceBots
	contextMessages int
	playbook        string
	agent           string
	access          accessSettings
	address         addressPolicy
	scope           sessionScope
}

type accessSettings struct {
	users    idSet
	roles    idSet
	channels idSet
	ignored  idSet
	allowAll bool
	bots     string
}

const selectMenuMax = 25

const turnRule = "Reminder: your work ends with this turn, nothing keeps running in the background. " +
	"Do the work now and report the result, or say what blocks you. Never announce work as \"started\" or \"in progress\" for later.\n\n"

const sharedRule = "Shared session: several people write in this conversation. " +
	"Each message is prefixed with [author]; answer whoever just spoke, without assuming it is always the same person.\n\n"

const channelContextHeader = "Channel context (earlier messages, all authors):\n"

type router struct {
	ctrl  func() contracts.SessionControl
	c     client
	binds *bindStore
	sinks *sinks
	msgs  messages
	log   *slog.Logger
	cfg   routerConfig
	langs sessionLanguages
	acl   *accessPolicy

	houseContent atomic.Bool
	routes       routeCache

	mu      sync.Mutex
	pending map[string]messageCreate
	jobs    map[string]job
}

type job struct {
	conv   string
	thread bool
	parent string
	slot   string
	shared bool
	agent  string
}

func newRouter(ctrl func() contracts.SessionControl, api client, binds *bindStore, sinks *sinks, msgs messages, log *slog.Logger, cfg routerConfig) *router {
	if cfg.address.appID == "" {
		cfg.address.appID = cfg.appID
	}
	return &router{
		ctrl:  ctrl,
		c:     api,
		binds: binds,
		sinks: sinks,
		msgs:  msgs,
		log:   log,
		cfg:   cfg,
		langs: sessionLanguages{ctrl: ctrl, binds: binds},
		acl: &accessPolicy{
			log:      log,
			owner:    cfg.owner,
			selfID:   cfg.appID,
			voices:   cfg.voices,
			users:    cfg.access.users,
			roles:    cfg.access.roles,
			channels: cfg.access.channels,
			ignored:  cfg.access.ignored,
			allowAll: cfg.access.allowAll,
			bots:     cfg.access.bots,
		},
		pending: make(map[string]messageCreate),
		jobs:    make(map[string]job),
	}
}

func (r *router) chatType(conv string, direct bool) string {
	switch {
	case direct:
		return chatDM
	case r.binds.IsThread(addressOf(conv).channel):
		return chatThread
	default:
		return chatChannel
	}
}

func (r *router) mode(conv, parent string) string {
	if v := r.binds.Mode(addressOf(conv).channel); v != "" {
		return v
	}
	if parent != "" {
		return r.binds.Mode(parent)
	}
	return normalMode
}

func (r *router) onMessage(ctx context.Context, msg messageCreate) {
	ctrl := r.ctrl()
	if ctrl == nil {
		return
	}
	if r.addressesAgentBot(msg) {
		return
	}
	parent := r.binds.Parent(msg.ChannelID)
	if !r.admittedBeforeRouting(msg, parent) {
		return
	}
	msg, ok := r.hydrated(ctx, ctrl, msg, parent, msg.ChannelID)
	if !ok {
		return
	}
	if r.voiceCaptured(ctx, ctrl, msg, parent) {
		return
	}
	if r.pendingAgentHolds(ctrl, msg, parent) {
		return
	}
	if !r.answersAsHouse(msg, parent) {
		return
	}
	chatType := r.chatType(msg.ChannelID, msg.direct())
	slot := r.cfg.scope.slot(chatType, msg.ChannelID, msg.Author.ID)
	if session := r.binds.Session(slot); session != "" {
		if r.fork(ctx, ctrl, session, msg) {
			return
		}
		if r.submit(ctx, ctrl, session, msg.ChannelID, msg, false, r.cfg.scope.multiUser(chatType)) {
			return
		}
		if err := r.binds.Unbind(slot); err != nil {
			r.noteSaveFailure(ctx, msg.ChannelID, err)
		}
	}
	r.ask(ctx, ctrl, msg)
}

func (r *router) admittedBeforeRouting(msg messageCreate, parent string) bool {
	return r.mode(msg.ChannelID, parent) != offMode && r.acl.permits(msg, parent, true)
}

func (r *router) answersAsHouse(msg messageCreate, parent string) bool {
	mode := r.mode(msg.ChannelID, parent)
	chatType := r.chatType(msg.ChannelID, msg.direct())
	if mode != ambientMode && !r.cfg.address.addressed(msg, chatType, parent) {
		return false
	}
	return r.acl.permits(msg, parent, r.cfg.address.mentioned(msg))
}

const hydrateWindow = 5

func (r *router) hydrated(ctx context.Context, ctrl contracts.SessionControl, msg messageCreate, parent, conv string) (messageCreate, bool) {
	if !msg.blank() {
		return msg, true
	}
	if !r.blankMayMatter(ctrl, msg, parent) {
		return msg, false
	}
	msg = r.hydrate(ctx, msg, conv)
	if msg.blank() {
		r.log.Debug("blanked message dropped, content unreadable", "channel", msg.ChannelID, "message", msg.ID)
		return msg, false
	}
	return msg, true
}

func (r *router) hydrate(ctx context.Context, msg messageCreate, conv string) messageCreate {
	msgs, err := r.c.ReadMessages(ctx, conv, hydrateWindow, "")
	if err != nil {
		r.log.Warn("read blanked message failed", "err", err)
		return msg
	}
	for i := range msgs {
		prev := msgs[i]
		if prev.ID == msg.ID {
			msg.Content, msg.Attachments = prev.Content, prev.Attachments
			break
		}
	}
	return msg
}

func (r *router) ask(ctx context.Context, ctrl contracts.SessionControl, msg messageCreate) {
	r.askPlaced(ctx, ctrl, msg, func() job { return r.place(ctx, msg) })
}

func (r *router) askPlaced(ctx context.Context, ctrl contracts.SessionControl, msg messageCreate, place func() job) {
	repos, err := ctrl.Repos(ctx)
	if err != nil {
		r.log.Warn("list repos failed", "err", err)
	}
	if len(repos) == 0 {
		r.post(ctx, msg.ChannelID, r.textsIn(msg.ChannelID).say("discord.repo.none", nil))
		return
	}
	task := place()
	r.sinks.at(task.conv).ack(ctx, msg.ChannelID, msg.ID, msg.Author.ID)
	if r.bindKnownRepo(ctx, ctrl, task, msg, repos) {
		return
	}
	r.askRepo(ctx, task, msg, repos)
}

func (r *router) noteSaveFailure(ctx context.Context, conv string, err error) {
	r.log.Warn("bind store save failed", "conv", conv, "err", err)
	texts := r.textsIn(conv)
	reason := texts.say("discord.bindings.write_failed", nil)
	if errors.Is(err, errStoreLocked) {
		reason = texts.say("discord.bindings.unreadable", nil)
	}
	r.sinks.at(conv).fail(ctx, reason)
}

func (r *router) bind(ctx context.Context, ctrl contracts.SessionControl, task job, value string, msg messageCreate, buffered bool) string {
	if task.slot == "" {
		task.slot = task.conv
	}
	agent := r.cfg.agent
	if task.agent != "" {
		agent = task.agent
	}
	spec := contracts.CreateSession{
		Name:      sessionNameFor(task.slot),
		ChannelID: task.conv,
		Gateways:  []string{gatewayKind},
		Agent:     agent,
	}
	if target, ok := strings.CutPrefix(value, "local:"); ok {
		spec.Project = target
	} else if target, ok := strings.CutPrefix(value, "remote:"); ok {
		spec.Clone = target
	} else {
		return r.msgs.say("discord.bind.bad_choice", nil)
	}
	if _, err := ctrl.Create(ctx, spec); err != nil {
		return r.msgs.noticeText(r.msgs.lang, contracts.NoticeOf(err))
	}
	save := func(slot, session string) error { return r.binds.Bind(slot, session) }
	if task.thread {
		save = func(slot, session string) error { return r.binds.BindThread(slot, task.conv, task.parent, session) }
	}
	if err := save(task.slot, spec.Name); err != nil {
		r.noteSaveFailure(ctx, task.conv, err)
	}
	if r.binds.Mode(task.parent) == supportMode {
		if err := r.binds.SetRepo(task.parent, value); err != nil {
			r.noteSaveFailure(ctx, task.conv, err)
		}
	}
	if err := r.binds.SetLastRepo(value); err != nil {
		r.noteSaveFailure(ctx, task.conv, err)
	}
	if buffered && !r.submit(ctx, ctrl, spec.Name, task.conv, msg, true, task.shared) {
		r.log.Warn("buffered message not delivered to the new session", "session", spec.Name, "conv", task.conv)
		r.sinks.at(task.conv).fail(ctx, r.textsIn(task.conv).say("discord.undelivered", nil))
	}
	return ""
}

func (r *router) onBindPick(ctx context.Context, channel, user, value string) string {
	ctrl := r.ctrl()
	if ctrl == nil {
		return r.msgs.say("discord.control.unavailable", nil)
	}
	r.mu.Lock()
	pending, buffered := r.pending[channel]
	owner := ""
	if buffered {
		owner = pending.Author.ID
	}
	if owner != "" && user != "" && owner != user {
		r.mu.Unlock()
		return r.msgs.say("discord.menu.owned_by", map[string]any{mentionArg: mention(owner)})
	}
	if owner == "" && !r.acl.authorizes(messageCreate{Author: dctl.Author{ID: user}}) {
		r.mu.Unlock()
		return r.msgs.say("discord.menu.denied", nil)
	}
	task, known := r.jobs[channel]
	delete(r.pending, channel)
	delete(r.jobs, channel)
	r.mu.Unlock()
	if !known {
		task = r.replaceLostJob(channel, user)
	}

	if failure := r.bind(ctx, ctrl, task, value, pending, buffered); failure != "" {
		r.sinks.at(task.conv).fail(ctx, "")
		return failure
	}
	return r.msgs.say("discord.repo.started", map[string]any{"Repo": neutralise(strings.TrimPrefix(strings.TrimPrefix(value, "local:"), "remote:"))})
}

func (r *router) replaceLostJob(channel, user string) job {
	addr := addressOf(channel)
	parent := r.binds.Parent(addr.channel)
	thread := addr.agent == "" && r.binds.IsThread(addr.channel)
	if parent == "" {
		parent = addr.channel
	}
	chatType := chatChannel
	if thread {
		chatType = chatThread
	}
	return job{
		conv:   channel,
		parent: parent,
		thread: thread,
		slot:   r.cfg.scope.slot(chatType, channel, user),
		shared: r.cfg.scope.multiUser(chatType),
		agent:  addr.agent,
	}
}

func (r *router) onChoicePick(_ context.Context, conv, user string, direct bool, value string) string {
	ctrl := r.ctrl()
	if ctrl == nil || !ctrl.Pick(r.sessionOf(ctrl, conv, user, direct), value) {
		return r.msgs.say("discord.choice.gone", nil)
	}
	return ""
}

func (r *router) sessionOf(ctrl contracts.SessionControl, conv, user string, direct bool) string {
	if session := r.boundSession(ctrl, conv, user, direct); session != "" {
		return session
	}
	return conv
}

func (r *router) boundSession(ctrl contracts.SessionControl, conv, user string, direct bool) string {
	if s := r.binds.Session(r.cfg.scope.slot(r.chatType(conv, direct), conv, user)); s != "" {
		return s
	}
	if s := r.binds.Session(conv); s != "" {
		return s
	}
	items := ctrl.Sessions()
	for i := range items {
		item := items[i]
		if item.ChannelID == conv {
			return item.Name
		}
	}
	return ""
}

func (r *router) submit(ctx context.Context, ctrl contracts.SessionControl, session, conv string, msg messageCreate, opening, shared bool) bool {
	ref := r.reference(ctx, msg)
	inbound := contracts.Inbound{
		Conversation: contracts.Conversation{Gateway: gatewayKind, ID: conv},
		Author:       msg.Author.Username,
		AuthorID:     msg.Author.ID,
		Text:         r.compose(ctx, msg, ref, opening, shared),
		Attachments:  attachmentsOf(msg, ref),
		MessageID:    contracts.MessageID(msg.ID),
	}
	if !ctrl.Submit(session, inbound) {
		return false
	}
	r.sinks.at(conv).noteUser(ctx, msg.ChannelID, msg.ID, msg.Author.ID)
	return true
}

func (r *router) compose(ctx context.Context, msg messageCreate, ref *dctl.Message, opening, shared bool) string {
	var parts []string
	if shared && opening {
		parts = append(parts, sharedRule)
	}
	if lines := r.context(ctx, msg); lines != "" {
		parts = append(parts, channelContextHeader, lines, "\n---\n\n")
	}
	if q := quote(ref); q != "" {
		parts = append(parts, q, "\n---\n\n")
	}
	if opening && r.cfg.playbook != "" {
		parts = append(parts, fmt.Sprintf("To finish this work, follow the skill %q.\n\n", r.cfg.playbook))
	}
	parts = append(parts, turnRule)
	if shared {
		parts = append(parts, "["+msg.Author.Username+"] ")
	}
	parts = append(parts, msg.Content)
	return strings.Join(parts, "")
}

func (r *router) context(ctx context.Context, msg messageCreate) string {
	if r.cfg.contextMessages <= 0 {
		return ""
	}
	msgs, err := r.c.ReadMessages(ctx, msg.ChannelID, r.cfg.contextMessages, "")
	if err != nil {
		r.log.Warn("context read failed", "err", err)
		return ""
	}
	var builder strings.Builder
	for i := range msgs {
		prev := msgs[i]
		if prev.ID == msg.ID {
			continue
		}
		if body := flattenLines(strings.TrimSpace(prev.Content)); body != "" {
			fmt.Fprintf(&builder, "%s: %s\n", prev.Author.Username, body)
		}
		for _, line := range describe(prev) {
			fmt.Fprintf(&builder, "%s: %s\n", prev.Author.Username, line)
		}
	}
	return builder.String()
}

func (r *router) textsIn(conv string) messages {
	return r.langs.messagesFor(conv, r.msgs)
}

func (r *router) post(ctx context.Context, channel, text string) {
	for _, part := range splitText(text, r.cfg.voices.textLimit(channel)) {
		if _, err := r.c.Send(ctx, channel, part); err != nil {
			r.log.Warn("post failed", "err", err)
			return
		}
	}
}
