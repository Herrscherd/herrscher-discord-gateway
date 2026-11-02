package discord

import (
	"context"
	"fmt"
	"os"
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

func newRouter(ctrl func() contracts.SessionControl, c client, binds *bindStore, sinks *sinks, cfg routerConfig) *router {
	if cfg.address.appID == "" {
		cfg.address.appID = cfg.appID
	}
	return &router{
		ctrl:  ctrl,
		c:     c,
		binds: binds,
		sinks: sinks,
		cfg:   cfg,
		acl: &accessPolicy{
			owner:    cfg.owner,
			selfID:   cfg.appID,
			users:    cfg.access.users,
			roles:    cfg.access.roles,
			channels: cfg.access.channels,
			ignored:  cfg.access.ignored,
			allowAll: cfg.access.allowAll,
			bots:     cfg.access.bots,
		},
		pending: map[string]messageCreate{},
		jobs:    map[string]job{},
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

func (r *router) onMessage(ctx context.Context, m messageCreate) {
	ctrl := r.ctrl()
	if ctrl == nil {
		return
	}
	parent := r.binds.Parent(m.ChannelID)
	mode := r.mode(m.ChannelID, parent)
	if mode == offMode {
		return
	}
	chatType := r.chatType(m.ChannelID, m.direct())
	if mode != ambientMode && !r.cfg.address.addressed(m, chatType, parent) {
		return
	}
	if !r.acl.permits(m, parent, r.cfg.address.mentioned(m)) {
		return
	}
	m = r.hydrate(ctx, m)
	slot := r.cfg.scope.slot(chatType, m.ChannelID, m.Author.ID)
	if session := r.binds.Session(slot); session != "" {
		if r.fork(ctx, ctrl, session, m) {
			return
		}
		if r.submit(ctx, ctrl, session, m.ChannelID, m, false, r.cfg.scope.multiUser(chatType)) {
			return
		}
		if err := r.binds.Unbind(slot); err != nil {
			warnf("unbind %s: %v", slot, err)
		}
	}
	r.ask(ctx, ctrl, m)
}

const hydrateWindow = 5

func (r *router) hydrate(ctx context.Context, m messageCreate) messageCreate {
	if m.Content != "" || len(m.Attachments) > 0 {
		return m
	}
	msgs, err := r.c.ReadMessages(ctx, m.ChannelID, hydrateWindow, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: read blanked message: %v\n", err)
		return m
	}
	for _, prev := range msgs {
		if prev.ID == m.ID {
			m.Content, m.Attachments = prev.Content, prev.Attachments
			break
		}
	}
	return m
}

func (r *router) ask(ctx context.Context, ctrl contracts.SessionControl, m messageCreate) {
	repos, err := ctrl.Repos(ctx)
	if err != nil || len(repos) == 0 {
		r.post(ctx, m.ChannelID, "je ne trouve aucun repo sur lequel travailler, vérifie le workspace ou l'auth de la forge")
		return
	}
	j := r.place(ctx, m)
	r.sinks.at(j.conv).ack(m.ChannelID, m.ID, m.Author.ID)

	if value := r.binds.Repo(j.parent); value != "" {
		if msg := r.bind(ctx, ctrl, j, value, m, true); msg != "" {
			r.sinks.at(j.conv).fail(msg)
		}
		return
	}

	if repo, ok := matchRepo(m.Content, repos); ok {
		if msg := r.bind(ctx, ctrl, j, repoValue(repo), m, true); msg != "" {
			r.sinks.at(j.conv).fail(msg)
		}
		return
	}

	if last := r.binds.LastRepo(); last != "" && knownRepo(last, repos) {
		if msg := r.bind(ctx, ctrl, j, last, m, true); msg != "" {
			r.sinks.at(j.conv).fail(msg)
			return
		}
		r.post(ctx, j.conv, fmt.Sprintf(
			"je repars sur **%s**, le dernier repo sur lequel tu m'as mis, `/session close` ici pour repartir d'ailleurs",
			repoLabel(last)))
		return
	}

	conv := j.conv
	r.mu.Lock()
	r.pending[conv] = m
	r.jobs[conv] = j
	r.mu.Unlock()

	opts := make([]dctl.SelectOption, 0, len(repos))
	for _, repo := range repos {
		if len(opts) == selectMenuMax {
			break
		}
		opts = append(opts, dctl.SelectOption{Label: repo.Name, Value: repoValue(repo), Description: repo.Description})
	}
	prompt := "sur quel repo je travaille ? (je ne poserai la question qu'une fois)"
	if len(repos) > selectMenuMax {
		prompt = fmt.Sprintf("%s, %d repos, les %d premiers sont listés", prompt, len(repos), selectMenuMax)
	}
	replyTo := m.ID
	if conv != m.ChannelID {
		replyTo = ""
	}
	if _, err := r.c.SendSelectMenu(ctx, conv, replyTo, prompt, BindCustomID(conv), opts); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: repo menu: %v\n", err)
		r.mu.Lock()
		delete(r.pending, conv)
		delete(r.jobs, conv)
		r.mu.Unlock()
		r.sinks.at(conv).fail("je n'ai pas pu poster le menu des repos ici, reformule en nommant le repo, ou vérifie mes permissions dans ce salon")
	}
}

func (r *router) place(ctx context.Context, m messageCreate) job {
	j := r.conversation(ctx, m)
	chatType := chatChannel
	switch {
	case m.direct():
		chatType = chatDM
	case j.thread:
		chatType = chatThread
	}
	j.slot = r.cfg.scope.slot(chatType, j.conv, m.Author.ID)
	j.shared = r.cfg.scope.multiUser(chatType)
	return j
}

func (r *router) conversation(ctx context.Context, m messageCreate) job {
	here := job{conv: m.ChannelID, parent: m.ChannelID}
	if r.binds.IsThread(m.ChannelID) {
		here.thread = true
		return here
	}
	if r.binds.Mode(m.ChannelID) == supportMode {
		id, err := r.c.StartThread(ctx, m.ChannelID, m.ID, threadName(m.Content))
		if err == nil && id != "" {
			r.adoptThread(m.ChannelID, id)
			return job{conv: id, thread: true, parent: m.ChannelID}
		}
		fmt.Fprintf(os.Stderr, "discord gateway: support thread in channel %s: %v\n", m.ChannelID, err)
		r.post(ctx, m.ChannelID, "je n'ai pas pu ouvrir de fil ici, il me manque « Créer des fils publics » dans ce salon. Je réponds ici, et ce salon ne portera qu'une conversation à la fois.")
		return here
	}
	if !wantsThread(m.Content) {
		return here
	}
	id, err := r.c.CreatePrivateThread(ctx, m.ChannelID, threadName(m.Content))
	if err == nil && id != "" {
		if err = r.c.AddThreadMember(ctx, id, m.Author.ID); err == nil {
			r.adoptThread(m.ChannelID, id)
			r.post(ctx, m.ChannelID, "je continue en privé dans <#"+id+">")
			return job{conv: id, thread: true, parent: m.ChannelID}
		}
	}
	fmt.Fprintf(os.Stderr, "discord gateway: private thread in channel %s: %v\n", m.ChannelID, err)
	r.post(ctx, m.ChannelID, "je n'ai pas pu ouvrir de fil privé ici, il me manque « Créer des fils privés » dans ce salon (une permission de salon prime sur celle du rôle). Je réponds ici.")
	return here
}

func (r *router) adoptThread(channel, thread string) {
	if err := r.binds.MarkThread(thread, channel); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: bind store save failed: %v\n", err)
	}
	lv := r.binds.Level(channel)
	if lv == "" {
		return
	}
	if err := r.binds.SetLevel(thread, lv); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: bind store save failed: %v\n", err)
	}
}

func (r *router) fork(ctx context.Context, ctrl contracts.SessionControl, session string, m messageCreate) bool {
	if !wantsThread(m.Content) || r.binds.IsThread(m.ChannelID) {
		return false
	}
	value, ok := repoOf(ctrl, session)
	if !ok {
		return false
	}
	j := r.place(ctx, m)
	if !j.thread {
		return false
	}
	r.sinks.at(j.conv).ack(m.ChannelID, m.ID, m.Author.ID)
	if msg := r.bind(ctx, ctrl, j, value, m, true); msg != "" {
		r.sinks.at(j.conv).fail(msg)
	}
	return true
}

func repoOf(ctrl contracts.SessionControl, session string) (string, bool) {
	for _, si := range ctrl.Sessions() {
		if si.Name != session {
			continue
		}
		if si.Project == "" {
			return "", false
		}
		return "local:" + si.Project, true
	}
	return "", false
}

func (r *router) bind(ctx context.Context, ctrl contracts.SessionControl, j job, value string, m messageCreate, buffered bool) string {
	if j.slot == "" {
		j.slot = j.conv
	}
	spec := contracts.CreateSession{
		Name:      sessionNameFor(j.slot),
		ChannelID: j.conv,
		Gateways:  []string{"discord"},
		Agent:     r.cfg.agent,
	}
	if target, ok := strings.CutPrefix(value, "local:"); ok {
		spec.Project = target
	} else if target, ok := strings.CutPrefix(value, "remote:"); ok {
		spec.Clone = target
	} else {
		return "choix illisible"
	}
	if _, err := ctrl.Create(ctx, spec); err != nil {
		return "création de session impossible : " + err.Error()
	}
	save := func(slot, session string) error { return r.binds.Bind(slot, session) }
	if j.thread {
		save = func(slot, session string) error { return r.binds.BindThread(slot, j.conv, j.parent, session) }
	}
	if err := save(j.slot, spec.Name); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: bind store save failed: %v\n", err)
	}
	if r.binds.Mode(j.parent) == supportMode {
		if err := r.binds.SetRepo(j.parent, value); err != nil {
			fmt.Fprintf(os.Stderr, "discord gateway: bind store save failed: %v\n", err)
		}
	}
	if err := r.binds.SetLastRepo(value); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: bind store save failed: %v\n", err)
	}
	if buffered {
		r.submit(ctx, ctrl, spec.Name, j.conv, m, true, j.shared)
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

func repoValue(r contracts.RepoRef) string {
	if r.Local {
		return "local:" + r.Name
	}
	return "remote:" + r.Name
}

func (r *router) onBindPick(ctx context.Context, channel, user, value string) string {
	ctrl := r.ctrl()
	if ctrl == nil {
		return "le contrôleur de sessions n'est pas encore prêt"
	}
	r.mu.Lock()
	m, buffered := r.pending[channel]
	owner := ""
	if buffered {
		owner = m.Author.ID
	}
	if owner != "" && user != "" && owner != user {
		r.mu.Unlock()
		return "ce menu répond à la question de <@" + owner + ">"
	}
	if owner == "" && !r.acl.authorizes(messageCreate{Author: dctl.Author{ID: user}}) {
		r.mu.Unlock()
		return "tu n'es pas autorisé à répondre à ce menu"
	}
	j, known := r.jobs[channel]
	delete(r.pending, channel)
	delete(r.jobs, channel)
	r.mu.Unlock()
	if !known {
		j = r.replaceLostJob(channel, user)
	}

	if msg := r.bind(ctx, ctrl, j, value, m, buffered); msg != "" {
		r.sinks.at(j.conv).fail("")
		return msg
	}
	return "c'est parti sur " + strings.TrimPrefix(strings.TrimPrefix(value, "local:"), "remote:")
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

func (r *router) onChoicePick(_ context.Context, id, user string, direct bool, value string) string {
	ctrl := r.ctrl()
	if ctrl == nil || !ctrl.Pick(r.sessionOf(ctrl, id, user, direct), value) {
		return "cette session n'est plus active"
	}
	return ""
}

func (r *router) sessionOf(ctrl contracts.SessionControl, id, user string, direct bool) string {
	if s := r.binds.Session(r.cfg.scope.slot(r.chatType(id, direct), id, user)); s != "" {
		return s
	}
	if s := r.binds.Session(id); s != "" {
		return s
	}
	for _, si := range ctrl.Sessions() {
		if si.ChannelID == id {
			return si.Name
		}
	}
	return id
}

func (r *router) submit(ctx context.Context, ctrl contracts.SessionControl, session, conv string, m messageCreate, opening, shared bool) bool {
	ref := r.reference(ctx, m)
	in := contracts.Inbound{
		Conversation: contracts.Conversation{Gateway: "discord", ID: conv},
		Author:       m.Author.Username,
		AuthorID:     m.Author.ID,
		Text:         r.compose(ctx, m, ref, opening, shared),
		Attachments:  attachmentsOf(m, ref),
		MessageID:    contracts.MessageID(m.ID),
	}
	if !ctrl.Submit(session, in) {
		return false
	}
	r.sinks.at(conv).noteUser(m.ChannelID, m.ID, m.Author.ID)
	return true
}

func (r *router) compose(ctx context.Context, m messageCreate, ref *dctl.Message, opening, shared bool) string {
	var b strings.Builder
	if shared && opening {
		b.WriteString(sharedRule)
	}
	if lines := r.context(ctx, m); lines != "" {
		b.WriteString("Contexte du salon (messages précédents, tous auteurs) :\n")
		b.WriteString(lines)
		b.WriteString("\n---\n\n")
	}
	if q := quote(ref); q != "" {
		b.WriteString(q)
		b.WriteString("\n---\n\n")
	}
	if opening && r.cfg.playbook != "" {
		fmt.Fprintf(&b, "Pour finir ce travail, suis la skill %q.\n\n", r.cfg.playbook)
	}
	b.WriteString(turnRule)
	if shared {
		fmt.Fprintf(&b, "[%s] ", m.Author.Username)
	}
	b.WriteString(m.Content)
	return b.String()
}

func (r *router) context(ctx context.Context, m messageCreate) string {
	if r.cfg.contextMessages <= 0 {
		return ""
	}
	msgs, err := r.c.ReadMessages(ctx, m.ChannelID, r.cfg.contextMessages, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: context read: %v\n", err)
		return ""
	}
	var b strings.Builder
	for _, prev := range msgs {
		if prev.ID == m.ID {
			continue
		}
		if body := flattenLines(strings.TrimSpace(prev.Content)); body != "" {
			fmt.Fprintf(&b, "%s: %s\n", prev.Author.Username, body)
		}
		for _, line := range describe(prev) {
			fmt.Fprintf(&b, "%s: %s\n", prev.Author.Username, line)
		}
	}
	return b.String()
}

func (r *router) post(ctx context.Context, channel, text string) {
	if _, err := r.c.Send(ctx, channel, text); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: post: %v\n", err)
	}
}
