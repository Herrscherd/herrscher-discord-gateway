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

// routerConfig is the plugin's own policy, read once from plugin config.
type routerConfig struct {
	owner           string // Discord user id the bot obeys
	appID           string // the bot's own application id
	contextMessages int    // how many prior channel messages to carry as context
	playbook        string // skill name the opening turn is told to follow
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

// selectMenuMax is Discord's hard cap on options in one select menu.
const selectMenuMax = 25

// turnRule is prepended to every turn, not just the opening one. Nothing the
// agent starts outlives the turn — there is no background runner behind this
// gateway — and an agent that announces "I've started the investigation" leaves
// the operator waiting on work that stopped the moment the turn ended. Repeating
// it each turn is deliberate: the rule matters most deep in a conversation,
// which is exactly where an opening-only instruction has faded.
const turnRule = "Rappel : ton travail s'arrête à la fin de ce tour, rien ne continue en tâche de fond. " +
	"Fais le travail maintenant et rends le résultat, ou dis ce qui bloque — n'annonce jamais un travail « lancé » ou « en cours » pour plus tard.\n\n"

const sharedRule = "Session partagée : plusieurs personnes écrivent dans cette conversation. " +
	"Chaque message est préfixé par [auteur] ; réponds à qui vient de parler, sans supposer que c'est toujours la même personne.\n\n"

// router turns a Discord message into a core turn. It owns every Discord-shaped
// decision the flow needs — who may trigger, which session a channel belongs to,
// what context to carry, how a repo question is asked and answered — so the core
// only ever receives a neutral Inbound for a named session.
type router struct {
	ctrl  func() contracts.SessionControl
	c     client
	binds *bindStore
	sinks *sinks
	cfg   routerConfig
	acl   *accessPolicy

	mu      sync.Mutex
	pending map[string]messageCreate // conversation id -> the ping awaiting a repo answer
	jobs    map[string]job           // conversation id -> where that ping's job lives
}

// job is where one piece of work happens: the conversation it runs in, whether
// the gateway opened that conversation, and the channel it was asked from. The
// parent is what the repo answer is remembered on — a support channel opens a
// conversation per ping, and the answer belongs to the room, not to one of them.
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
		// A ping that asks for a private thread gets one here too. Only ask()
		// used to read that request, so a channel that already had a session
		// swallowed it and answered in public — which is the one outcome asking
		// for privacy is about.
		if r.fork(ctx, ctrl, session, m) {
			return
		}
		if r.submit(ctx, ctrl, session, m.ChannelID, m, false, r.cfg.scope.multiUser(chatType)) {
			return
		}
		// The session is gone (daemon restarted, session closed out of band).
		// Drop the stale binding and fall through to ask again, so a restart
		// mid-conversation costs one question rather than silence.
		_ = r.binds.Unbind(slot)
	}
	r.ask(ctx, ctrl, m)
}

// hydrateWindow is how far back a blanked message is looked for. It only has to
// cover the messages posted between the dispatch and the REST read.
const hydrateWindow = 5

// hydrate fills in a body the websocket dispatch did not carry. The gateway
// identifies without the privileged MESSAGE_CONTENT intent, so Discord blanks
// `content` for everything that does not @mention the bot — which is every bare
// message in one of our private threads, the one place the bot answers without
// being mentioned. The same text is readable over REST, which the intent does
// not gate, so it costs one extra call in exactly that case and nothing at all
// when the dispatch already carried the text.
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

// ask opens a conversation for the ping: it picks where the job happens, then
// either binds the repo the message already names or buffers the ping and posts
// the repo question. Only the newest ping is kept: answering the menu replays
// one message, and replaying a stale one would be worse than dropping it.
func (r *router) ask(ctx context.Context, ctrl contracts.SessionControl, m messageCreate) {
	repos, err := ctrl.Repos(ctx)
	if err != nil || len(repos) == 0 {
		r.post(ctx, m.ChannelID, "je ne trouve aucun repo sur lequel travailler — vérifie le workspace ou l'auth de la forge")
		return
	}
	// Where the job lives: this channel, or a private thread when the ping asks
	// for one. Everything below is keyed on it — the binding, the menu, the
	// session's own channel — so the whole job stays in one place.
	j := r.place(ctx, m)
	// The ping is taken. Mark it now, in the channel it was written in: behind a
	// question, or at the silent level, there is nothing else to show, and an
	// unmarked ping reads as ignored.
	r.sinks.at(j.conv).ack(m.ChannelID, m.ID, m.Author.ID)

	// The room has already answered the repo question — a support channel asks it
	// once and every conversation opened in it inherits the answer.
	if value := r.binds.Repo(j.parent); value != "" {
		if msg := r.bind(ctx, ctrl, j, value, m, true); msg != "" {
			r.sinks.at(j.conv).fail(msg)
		}
		return
	}

	// The repo is usually named in the ping itself; asking anyway is friction.
	if repo, ok := matchRepo(m.Content, repos); ok {
		if msg := r.bind(ctx, ctrl, j, repoValue(repo), m, true); msg != "" {
			r.sinks.at(j.conv).fail(msg)
		}
		return
	}

	// Still nothing to go on here — but the operator has answered this question
	// before, somewhere else. Asking again for every new room turns a promise to
	// ask once into a toll on every conversation, and it is levied hardest on the
	// pings that are not about code at all: a plain question cannot name a repo,
	// so it always pays. Reuse the last answer and say which one, out loud, so a
	// wrong guess is visible in the same breath rather than discovered later.
	if last := r.binds.LastRepo(); last != "" && knownRepo(last, repos) {
		if msg := r.bind(ctx, ctrl, j, last, m, true); msg != "" {
			r.sinks.at(j.conv).fail(msg)
			return
		}
		r.post(ctx, j.conv, fmt.Sprintf(
			"je repars sur **%s**, le dernier repo sur lequel tu m'as mis — `/session close` ici pour repartir d'ailleurs",
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
		prompt = fmt.Sprintf("%s — %d repos, les %d premiers sont listés", prompt, len(repos), selectMenuMax)
	}
	// A reply only resolves inside its own channel: asked from a thread, the ping
	// is in the parent and there is nothing here to reply to.
	replyTo := m.ID
	if conv != m.ChannelID {
		replyTo = ""
	}
	if _, err := r.c.SendSelectMenu(ctx, conv, replyTo, prompt, BindCustomID(conv), opts); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: repo menu: %v\n", err)
		// The ping is marked taken and the question that was supposed to follow it
		// never arrived. Without this the ⏳ is the only thing the operator ever
		// gets: no menu, no answer, and no way to tell a slow turn from a dead one.
		r.mu.Lock()
		delete(r.pending, conv)
		delete(r.jobs, conv)
		r.mu.Unlock()
		r.sinks.at(conv).fail("je n'ai pas pu poster le menu des repos ici — reformule en nommant le repo, ou vérifie mes permissions dans ce salon")
	}
}

// conversation decides where this job happens. A support channel gives every
// ping a public thread of its own; elsewhere, a ping that asks for a thread gets
// a private one, with the operator added as its only human member and one line
// in the channel pointing at it — a room nobody can find is a room the work got
// lost in.
//
// Creating either can fail — a missing permission, a channel type with no threads —
// and the ping must still be answered, so the job falls back to the channel it
// was asked in. That fallback is said out loud rather than taken silently: work
// asked for in private landing in a room other people read is the one outcome
// the request was about.
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
	// Already inside a thread the gateway opened — its session died and is being
	// recreated. The job stays where it is: opening a thread inside a thread is
	// not a thing, and losing the flag would cost the thread its no-@mention rule.
	if r.binds.IsThread(m.ChannelID) {
		here.thread = true
		return here
	}
	// A support channel gives every ping a thread of its own, and a public one:
	// the room is where independent questions arrive, so the channel keeps a
	// readable trace of who asked what, the asker is a member of their own thread
	// without being added, and no "create private threads" permission is needed.
	if r.binds.Mode(m.ChannelID) == supportMode {
		id, err := r.c.StartThread(ctx, m.ChannelID, m.ID, threadName(m.Content))
		if err == nil && id != "" {
			r.adoptThread(m.ChannelID, id)
			return job{conv: id, thread: true, parent: m.ChannelID}
		}
		fmt.Fprintf(os.Stderr, "discord gateway: support thread in channel %s: %v\n", m.ChannelID, err)
		r.post(ctx, m.ChannelID, "je n'ai pas pu ouvrir de fil ici — il me manque « Créer des fils publics » dans ce salon. Je réponds ici, et ce salon ne portera qu'une conversation à la fois.")
		return here
	}
	if !wantsThread(m.Content) {
		return here
	}
	id, err := r.c.CreatePrivateThread(ctx, m.ChannelID, threadName(m.Content))
	if err == nil && id != "" {
		// A thread the operator is not a member of is a room only the bot can
		// read, which is no better than not having one.
		if err = r.c.AddThreadMember(ctx, id, m.Author.ID); err == nil {
			r.adoptThread(m.ChannelID, id)
			// Say where the work went. The answer will be posted in the thread, so
			// without a line here the channel shows a ping, a ⏳ that clears minutes
			// later, and nothing else — the operator has to guess that a room they
			// cannot see from here is where it happened. The link resolves only for
			// members of the thread, which is the operator and the bot.
			r.post(ctx, m.ChannelID, "je continue en privé dans <#"+id+">")
			return job{conv: id, thread: true, parent: m.ChannelID}
		}
	}
	// Name the channel. Discord answers a denied thread with "Missing Access",
	// which reads like the bot cannot see the channel at all — it is usually one
	// channel overriding a permission the role does hold, and without the id there
	// is nothing to go and look at.
	fmt.Fprintf(os.Stderr, "discord gateway: private thread in channel %s: %v\n", m.ChannelID, err)
	r.post(ctx, m.ChannelID, "je n'ai pas pu ouvrir de fil privé ici — il me manque « Créer des fils privés » dans ce salon (une permission de salon prime sur celle du rôle). Je réponds ici.")
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

// fork moves a job out of a channel that already has a session and into a
// private thread of its own, when the ping asks for one. The thread's session is
// created on the same repo the channel's session runs on: the operator answered
// that question when this channel was bound, and asking again for work they just
// described is friction. It reports whether it took the ping — false leaves the
// caller on the ordinary path, which answers in the channel.
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
		// The thread could not be opened, and conversation() already said so in
		// the channel. The job stays on the session already bound here: the one
		// fork would create is named after this same channel and would collide.
		return false
	}
	r.sinks.at(j.conv).ack(m.ChannelID, m.ID, m.Author.ID)
	if msg := r.bind(ctx, ctrl, j, value, m, true); msg != "" {
		r.sinks.at(j.conv).fail(msg)
	}
	return true
}

// repoOf encodes what a live session runs on as a menu value, so a thread forked
// off it is created through the same path a menu answer takes. It is always the
// local form: whatever the channel's session started from, remote clone
// included, is a checkout in the workspace by now and re-cloning it would be
// work for nothing. A session the controller no longer lists has nothing to
// inherit, and neither has one started from the bare workspace with no project —
// both fall back to asking.
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

// bind creates the session on the chosen repo, adopting conv, remembers the
// binding and replays the ping. It returns the text the operator should be told,
// empty when the session started and the work speaks for itself.
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
	// The room now knows what it works on, so the next ping opens its thread
	// without asking again. Only in support mode: elsewhere a channel holds one
	// conversation, and the answer it gave is already carried by the binding.
	if r.binds.Mode(j.parent) == supportMode {
		if err := r.binds.SetRepo(j.parent, value); err != nil {
			fmt.Fprintf(os.Stderr, "discord gateway: bind store save failed: %v\n", err)
		}
	}
	// And the operator now has a current repo, which the next room inherits
	// instead of asking again.
	if err := r.binds.SetLastRepo(value); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: bind store save failed: %v\n", err)
	}
	if buffered {
		r.submit(ctx, ctrl, spec.Name, j.conv, m, true, j.shared)
	}
	return ""
}

// knownRepo reports whether a remembered menu value is still one of the repos on
// offer. A repo that was renamed, moved, or lost with its host must fall back to
// the question: binding a session to a target that no longer resolves would fail
// somewhere deeper, with an error about a path instead of a menu.
func knownRepo(value string, repos []contracts.RepoRef) bool {
	for _, repo := range repos {
		if repoValue(repo) == value {
			return true
		}
	}
	return false
}

// repoLabel renders a menu value as the operator wrote it — the encoding prefix
// is an internal detail and reads as noise in a channel.
func repoLabel(value string) string {
	if _, name, found := strings.Cut(value, ":"); found {
		return name
	}
	return value
}

// repoValue encodes a RepoRef into a menu value that survives the round trip and
// says which CreateSession field it belongs in.
func repoValue(r contracts.RepoRef) string {
	if r.Local {
		return "local:" + r.Name
	}
	return "remote:" + r.Name
}

// onBindPick answers the repo question: create the session on the picked target,
// adopting this channel, remember the binding, then replay the buffered ping. It
// returns the text the click is acknowledged with.
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
		// The click is answered with the reason, so the ping only needs its ⏳
		// turned into a ❌ — repeating the text under it would say it twice.
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

// onChoicePick routes an agent's pending-choice answer back to its session. It
// returns the acknowledgement text, empty when the pick landed.
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

// submit assembles the neutral Inbound and hands it to the core. conv is where
// the answer goes, which is not always where the message came from: a job moved
// into a private thread is driven by pings that may still land in the parent
// channel. opening marks the first turn of a freshly created session, which is
// where the playbook is named. It reports whether a live session accepted it.
func (r *router) submit(ctx context.Context, ctrl contracts.SessionControl, session, conv string, m messageCreate, opening, shared bool) bool {
	// Resolved once: the message this ping replies to feeds both the turn text
	// and its attachments, and re-reading it for each would cost a second call.
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
	// The ⏳ ack belongs on the message that triggered this turn, in the channel
	// that message lives in — the poller used to do this from Read. The sink is
	// the one rendering the answer, which is a different channel once the job
	// moved into a thread.
	r.sinks.at(conv).noteUser(m.ChannelID, m.ID, m.Author.ID)
	return true
}

// compose builds the turn text: what everyone else has been saying in this
// channel, then the message the ping replies to, then the owner's actual
// instruction. Assembling context here is what keeps the core agnostic — it
// receives one opaque string. The quote sits closest to the instruction because
// that is what the instruction is usually about.
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

// context reads the channel's recent history over REST. This is not gated by the
// message-content intent — only gateway dispatches are — so the bot sees what
// everyone said without asking Discord for a privileged intent.
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
		// A message with no text is not an empty message: a bot reports through
		// embeds, and skipping those made a whole channel of them invisible.
		if body := flattenLines(strings.TrimSpace(prev.Content)); body != "" {
			fmt.Fprintf(&b, "%s: %s\n", prev.Author.Username, body)
		}
		for _, line := range describe(prev) {
			fmt.Fprintf(&b, "%s: %s\n", prev.Author.Username, line)
		}
	}
	return b.String()
}

// post is a best-effort operator-facing message in a channel.
func (r *router) post(ctx context.Context, channel, text string) {
	if _, err := r.c.Send(ctx, channel, text); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: post: %v\n", err)
	}
}
