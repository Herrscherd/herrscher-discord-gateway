package discord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

type routerConfig struct {
	owner           string
	appID           string
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

const turnRule = "Rappel : ton travail s'arrête à la fin de ce tour, rien ne continue en tâche de fond. " +
	"Fais le travail maintenant et rends le résultat, ou dis ce qui bloque, n'annonce jamais un travail « lancé » ou « en cours » pour plus tard.\n\n"

const sharedRule = "Session partagée : plusieurs personnes écrivent dans cette conversation. " +
	"Chaque message est préfixé par [auteur] ; réponds à qui vient de parler, sans supposer que c'est toujours la même personne.\n\n"

type router struct {
	ctrl  func() contracts.SessionControl
	c     client
	binds *bindStore
	sinks *sinks
	msgs  messages
	log   *slog.Logger
	cfg   routerConfig
	acl   *accessPolicy

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
		acl: &accessPolicy{
			log:      log,
			owner:    cfg.owner,
			selfID:   cfg.appID,
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
	case r.binds.IsThread(conv):
		return chatThread
	default:
		return chatChannel
	}
}

func (r *router) mode(conv, parent string) string {
	if v := r.binds.Mode(conv); v != "" {
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
	parent := r.binds.Parent(msg.ChannelID)
	mode := r.mode(msg.ChannelID, parent)
	if mode == offMode {
		return
	}
	chatType := r.chatType(msg.ChannelID, msg.direct())
	if mode != ambientMode && !r.cfg.address.addressed(msg, chatType, parent) {
		return
	}
	if !r.acl.permits(msg, parent, r.cfg.address.mentioned(msg)) {
		return
	}
	msg = r.hydrate(ctx, msg)
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

const hydrateWindow = 5

func (r *router) hydrate(ctx context.Context, msg messageCreate) messageCreate {
	if msg.Content != "" || len(msg.Attachments) > 0 {
		return msg
	}
	msgs, err := r.c.ReadMessages(ctx, msg.ChannelID, hydrateWindow, "")
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
	repos, err := ctrl.Repos(ctx)
	if err != nil {
		r.log.Warn("list repos failed", "err", err)
	}
	if len(repos) == 0 {
		r.post(ctx, msg.ChannelID, r.msgs.say("repo.none", nil))
		return
	}
	task := r.place(ctx, msg)
	r.sinks.at(task.conv).ack(ctx, msg.ChannelID, msg.ID, msg.Author.ID)
	if r.bindKnownRepo(ctx, ctrl, task, msg, repos) {
		return
	}
	r.askRepo(ctx, task, msg, repos)
}

func (r *router) bindKnownRepo(ctx context.Context, ctrl contracts.SessionControl, task job, msg messageCreate, repos []contracts.RepoRef) bool {
	if value := r.binds.Repo(task.parent); value != "" {
		r.bindOrFail(ctx, ctrl, task, value, msg)
		return true
	}
	if repo, ok := matchRepo(msg.Content, repos); ok {
		r.bindOrFail(ctx, ctrl, task, repoValue(repo), msg)
		return true
	}
	last := r.binds.LastRepo()
	if last == "" || !knownRepo(last, repos) {
		return false
	}
	if failure := r.bind(ctx, ctrl, task, last, msg, true); failure != "" {
		r.sinks.at(task.conv).fail(ctx, failure)
		return true
	}
	r.post(ctx, task.conv, r.msgs.say("repo.reused", map[string]any{"Repo": neutralise(repoLabel(last))}))
	return true
}

func (r *router) bindOrFail(ctx context.Context, ctrl contracts.SessionControl, task job, value string, m messageCreate) {
	if failure := r.bind(ctx, ctrl, task, value, m, true); failure != "" {
		r.sinks.at(task.conv).fail(ctx, failure)
	}
}

func (r *router) askRepo(ctx context.Context, task job, msg messageCreate, repos []contracts.RepoRef) {
	conv := task.conv
	r.mu.Lock()
	r.pending[conv] = msg
	r.jobs[conv] = task
	r.mu.Unlock()

	prompt := r.msgs.say("repo.ask", nil)
	if len(repos) > selectMenuMax {
		prompt = r.msgs.say("repo.ask_capped", map[string]any{"Question": prompt, countArg: len(repos), "Shown": selectMenuMax})
	}
	replyTo := msg.ID
	if conv != msg.ChannelID {
		replyTo = ""
	}
	if _, err := r.c.SendSelectMenu(ctx, conv, replyTo, prompt, BindCustomID(conv), repoOptions(repos)); err != nil {
		r.log.Warn("repo menu failed", "err", err)
		r.mu.Lock()
		delete(r.pending, conv)
		delete(r.jobs, conv)
		r.mu.Unlock()
		r.sinks.at(conv).fail(ctx, r.msgs.say("repo.menu_failed", nil))
	}
}

func repoOptions(repos []contracts.RepoRef) []dctl.SelectOption {
	opts := make([]dctl.SelectOption, 0, min(len(repos), selectMenuMax))
	for _, repo := range repos {
		if len(opts) == selectMenuMax {
			break
		}
		opts = append(opts, dctl.SelectOption{Label: repo.Name, Value: repoValue(repo), Description: repo.Description})
	}
	return opts
}

func (r *router) noteSaveFailure(ctx context.Context, conv string, err error) {
	r.log.Warn("bind store save failed", "conv", conv, "err", err)
	reason := r.msgs.say("bindings.write_failed", nil)
	if errors.Is(err, errStoreLocked) {
		reason = r.msgs.say("bindings.unreadable", nil)
	}
	r.sinks.at(conv).fail(ctx, reason)
}

func repoOf(ctrl contracts.SessionControl, session string) (string, bool) {
	items := ctrl.Sessions()
	for i := range items {
		item := items[i]
		if item.Name != session {
			continue
		}
		if item.Project == "" {
			return "", false
		}
		return "local:" + item.Project, true
	}
	return "", false
}

func (r *router) bind(ctx context.Context, ctrl contracts.SessionControl, task job, value string, msg messageCreate, buffered bool) string {
	if task.slot == "" {
		task.slot = task.conv
	}
	spec := contracts.CreateSession{
		Name:      sessionNameFor(task.slot),
		ChannelID: task.conv,
		Gateways:  []string{gatewayKind},
		Agent:     r.cfg.agent,
	}
	if target, ok := strings.CutPrefix(value, "local:"); ok {
		spec.Project = target
	} else if target, ok := strings.CutPrefix(value, "remote:"); ok {
		spec.Clone = target
	} else {
		return r.msgs.say("bind.bad_choice", nil)
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
		r.sinks.at(task.conv).fail(ctx, r.msgs.say("undelivered", nil))
	}
	return ""
}

func knownRepo(value string, repos []contracts.RepoRef) bool {
	for _, repo := range repos {
		if repoValue(repo) == value {
			return true
		}
	}
	return false
}

func repoLabel(value string) string {
	if _, name, found := strings.Cut(value, ":"); found {
		return name
	}
	return value
}

func repoValue(repo contracts.RepoRef) string {
	if repo.Local {
		return "local:" + repo.Name
	}
	return "remote:" + repo.Name
}

func (r *router) onBindPick(ctx context.Context, channel, user, value string) string {
	ctrl := r.ctrl()
	if ctrl == nil {
		return r.msgs.say("control.unavailable", nil)
	}
	r.mu.Lock()
	pending, buffered := r.pending[channel]
	owner := ""
	if buffered {
		owner = pending.Author.ID
	}
	if owner != "" && user != "" && owner != user {
		r.mu.Unlock()
		return r.msgs.say("menu.owned_by", map[string]any{mentionArg: mention(owner)})
	}
	if owner == "" && !r.acl.authorizes(messageCreate{Author: dctl.Author{ID: user}}) {
		r.mu.Unlock()
		return r.msgs.say("menu.denied", nil)
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
	return r.msgs.say("repo.started", map[string]any{"Repo": neutralise(strings.TrimPrefix(strings.TrimPrefix(value, "local:"), "remote:"))})
}

func (r *router) replaceLostJob(channel, user string) job {
	parent := r.binds.Parent(channel)
	thread := r.binds.IsThread(channel)
	if parent == "" {
		parent = channel
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
	}
}

func (r *router) onChoicePick(_ context.Context, conv, user string, direct bool, value string) string {
	ctrl := r.ctrl()
	if ctrl == nil || !ctrl.Pick(r.sessionOf(ctrl, conv, user, direct), value) {
		return r.msgs.say("choice.gone", nil)
	}
	return ""
}

func (r *router) sessionOf(ctrl contracts.SessionControl, conv, user string, direct bool) string {
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
	return conv
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
		parts = append(parts, "Contexte du salon (messages précédents, tous auteurs) :\n", lines, "\n---\n\n")
	}
	if q := quote(ref); q != "" {
		parts = append(parts, q, "\n---\n\n")
	}
	if opening && r.cfg.playbook != "" {
		parts = append(parts, fmt.Sprintf("Pour finir ce travail, suis la skill %q.\n\n", r.cfg.playbook))
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

func (r *router) post(ctx context.Context, channel, text string) {
	if _, err := r.c.Send(ctx, channel, text); err != nil {
		r.log.Warn("post failed", "err", err)
	}
}
