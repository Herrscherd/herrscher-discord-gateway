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
}

// selectMenuMax is Discord's hard cap on options in one select menu.
const selectMenuMax = 25

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
	trig  trigger

	mu      sync.Mutex
	pending map[string]messageCreate // channel id -> the ping awaiting a repo answer
}

func newRouter(ctrl func() contracts.SessionControl, c client, binds *bindStore, sinks *sinks, cfg routerConfig) *router {
	return &router{
		ctrl:    ctrl,
		c:       c,
		binds:   binds,
		sinks:   sinks,
		cfg:     cfg,
		trig:    trigger{owner: cfg.owner, appID: cfg.appID},
		pending: map[string]messageCreate{},
	}
}

// onMessage is the single entry point from the websocket. Everything that is not
// the owner addressing the bot stops here, before any core call.
func (r *router) onMessage(ctx context.Context, m messageCreate) {
	if !r.trig.fires(m) {
		return
	}
	ctrl := r.ctrl()
	if ctrl == nil {
		return
	}
	if session := r.binds.Session(m.ChannelID); session != "" {
		if r.submit(ctx, ctrl, session, m, false) {
			return
		}
		// The session is gone (daemon restarted, session closed out of band).
		// Drop the stale binding and fall through to ask again, so a restart
		// mid-conversation costs one question rather than silence.
		_ = r.binds.Unbind(m.ChannelID)
	}
	r.ask(ctx, ctrl, m)
}

// ask buffers the ping and posts the repo question. Only the newest ping is kept:
// answering the menu replays one message, and replaying a stale one would be
// worse than dropping it.
func (r *router) ask(ctx context.Context, ctrl contracts.SessionControl, m messageCreate) {
	repos, err := ctrl.Repos(ctx)
	if err != nil || len(repos) == 0 {
		r.post(ctx, m.ChannelID, "je ne trouve aucun repo sur lequel travailler — vérifie le workspace ou l'auth de la forge")
		return
	}
	r.mu.Lock()
	r.pending[m.ChannelID] = m
	r.mu.Unlock()

	opts := make([]dctl.SelectOption, 0, len(repos))
	for _, repo := range repos {
		if len(opts) == selectMenuMax {
			break
		}
		opts = append(opts, dctl.SelectOption{Label: repo.Name, Value: repoValue(repo), Description: repo.Description})
	}
	prompt := "sur quel repo je travaille dans ce salon ? (je ne poserai la question qu'une fois)"
	if len(repos) > selectMenuMax {
		prompt = fmt.Sprintf("%s — %d repos, les %d premiers sont listés", prompt, len(repos), selectMenuMax)
	}
	if _, err := r.c.SendSelectMenu(ctx, m.ChannelID, m.ID, prompt, BindCustomID(m.ChannelID), opts); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: repo menu: %v\n", err)
	}
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
func (r *router) onBindPick(ctx context.Context, channel, value string) string {
	ctrl := r.ctrl()
	if ctrl == nil {
		return "le contrôleur de sessions n'est pas encore prêt"
	}
	r.mu.Lock()
	m, buffered := r.pending[channel]
	delete(r.pending, channel)
	r.mu.Unlock()

	spec := contracts.CreateSession{
		Name:      sessionNameFor(channel),
		ChannelID: channel,
		Gateways:  []string{"discord"},
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
	if err := r.binds.Bind(channel, spec.Name); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: bind store save failed: %v\n", err)
	}
	if buffered {
		r.submit(ctx, ctrl, spec.Name, m, true)
	}
	return "c'est parti sur " + strings.TrimPrefix(strings.TrimPrefix(value, "local:"), "remote:")
}

// onChoicePick routes an agent's pending-choice answer back to its session. It
// returns the acknowledgement text, empty when the pick landed.
func (r *router) onChoicePick(_ context.Context, session, value string) string {
	ctrl := r.ctrl()
	if ctrl == nil || !ctrl.Pick(session, value) {
		return "cette session n'est plus active"
	}
	return ""
}

// submit assembles the neutral Inbound and hands it to the core. opening marks
// the first turn of a freshly created session, which is where the playbook is
// named. It reports whether a live session accepted it.
func (r *router) submit(ctx context.Context, ctrl contracts.SessionControl, session string, m messageCreate, opening bool) bool {
	in := contracts.Inbound{
		Conversation: contracts.Conversation{Gateway: "discord", ID: m.ChannelID},
		Author:       m.Author.Username,
		AuthorID:     m.Author.ID,
		Text:         r.compose(ctx, m, opening),
		Attachments:  attachmentsOf(m),
		MessageID:    contracts.MessageID(m.ID),
	}
	if !ctrl.Submit(session, in) {
		return false
	}
	// The ⏳ ack belongs on the message that triggered this turn, in its own
	// channel — the poller used to do this from Read.
	r.sinks.at(m.ChannelID).noteUser(m.ID)
	return true
}

// compose builds the turn text: what everyone else has been saying in this
// channel, then the owner's actual instruction. Assembling context here is what
// keeps the core agnostic — it receives one opaque string.
func (r *router) compose(ctx context.Context, m messageCreate, opening bool) string {
	var b strings.Builder
	if lines := r.context(ctx, m); lines != "" {
		b.WriteString("Contexte du salon (messages précédents, tous auteurs) :\n")
		b.WriteString(lines)
		b.WriteString("\n---\n\n")
	}
	if opening && r.cfg.playbook != "" {
		fmt.Fprintf(&b, "Pour finir ce travail, suis la skill %q.\n\n", r.cfg.playbook)
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
		if prev.ID == m.ID || strings.TrimSpace(prev.Content) == "" {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", prev.Author.Username, prev.Content)
	}
	return b.String()
}

// attachmentsOf maps the message's uploads to the neutral shape. The host, not
// the gateway, downloads them — against the session's own allowlist.
func attachmentsOf(m messageCreate) []contracts.Attachment {
	if len(m.Attachments) == 0 {
		return nil
	}
	out := make([]contracts.Attachment, 0, len(m.Attachments))
	for _, a := range m.Attachments {
		out = append(out, contracts.Attachment{
			Filename:    a.Filename,
			URL:         a.URL,
			ContentType: a.ContentType,
			Size:        a.Size,
		})
	}
	return out
}

// sessionNameFor derives a stable session name from a channel id, so a restart
// re-binds the same name and it can never collide with an operator-named session.
func sessionNameFor(channel string) string { return "ch-" + channel }

// post is a best-effort operator-facing message in a channel.
func (r *router) post(ctx context.Context, channel, text string) {
	if _, err := r.c.Send(ctx, channel, text); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: post: %v\n", err)
	}
}
