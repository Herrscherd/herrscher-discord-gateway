package discord

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

// slash owns every Discord slash command the gateway exposes. It translates an
// interaction into either a neutral argv it hands to the core SessionControl
// seam (session/service/set commands) or a mutation of the plugin-local allow
// store (allow lists). All command policy lives here on the gateway side: the
// core never learns the Discord command surface — only the neutral argv crosses
// the boundary via ctrl.Dispatch.
type slash struct {
	ctx    context.Context
	token  string
	ix     *dctl.Interactions
	reg    *dctl.Registry
	allow  *allowStore
	binds  *bindStore
	ctrl   contracts.SessionControl
	comp   acker
	router *router
}

// acker acknowledges a select-menu click so the dropdown collapses instead of
// spinning. *dctl.Components satisfies it; tests substitute a recorder.
type acker interface {
	Ack(ctx context.Context, id, token, content string) error
}

// newSlash builds the slash runtime and registers the command catalog + handlers
// on the interactions registry. ctrl is bound later (BindSessionControl) once the
// daemon hands the gateway its runtime session controller; router is assigned by
// the factory once the application id it needs has been resolved.
func newSlash(ctx context.Context, ix *dctl.Interactions, comp acker, token string, allow *allowStore, binds *bindStore) *slash {
	s := &slash{ctx: ctx, token: token, ix: ix, comp: comp, reg: ix.Registry(), allow: allow, binds: binds}
	s.reg.
		Add(commandSet(), s.handleSet).
		Add(commandSession(), s.handleSession).
		Add(commandService(), s.handleService).
		Add(commandAllow(), s.handleAllow).
		Add(commandStop(), s.handleStop).
		Add(commandVerbosity(), s.handleVerbosity).
		Add(commandMode(), s.handleMode).
		Autocomplete("session", s.autoSession)
	return s
}

// start syncs the command catalog to Discord and runs the gateway websocket loop
// until the daemon context is cancelled. It is called once the session
// controller is bound, so dispatch always has a live ctrl.
func (s *slash) start() {
	// The sync runs beside the websocket, not before it: Discord rate-limits the
	// command catalog hard, and a boot that lands on a 429 used to give up for the
	// lifetime of the process — which means a command added in a release is simply
	// never published, silently, and the operator types a name Discord has never
	// heard of. Retrying costs nothing, and the commands already registered keep
	// working while it does.
	go s.sync()
	newWS(s.token, s.onInteraction, s.onMessage).run(s.ctx)
}

// syncBackoff is how long to wait between attempts at publishing the command
// catalog. It grows because a 429 here is Discord saying the whole route is
// spent, and it stops: past a few minutes the answer is not a rate limit but
// something the operator has to look at.
var syncBackoff = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute}

func (s *slash) sync() { syncWithRetry(s.ctx, s.reg.Sync) }

func syncWithRetry(ctx context.Context, sync func(context.Context) error) {
	for attempt := 0; ; attempt++ {
		err := sync(ctx)
		if err == nil {
			return
		}
		if attempt == len(syncBackoff) {
			fmt.Fprintf(os.Stderr, "discord gateway: command sync gave up after %d attempts: %v\n", attempt+1, err)
			return
		}
		fmt.Fprintf(os.Stderr, "discord gateway: command sync: %v (retrying in %s)\n", err, syncBackoff[attempt])
		select {
		case <-time.After(syncBackoff[attempt]):
		case <-ctx.Done():
			return
		}
	}
}

// onMessage hands every MESSAGE_CREATE to the router, which decides whether it
// is the owner addressing the bot.
func (s *slash) onMessage(ctx context.Context, m messageCreate) {
	if s.router != nil {
		s.router.onMessage(ctx, m)
	}
}

// onComponent answers a select-menu click: a repo-binding menu creates the
// session, a session-choice menu answers the agent's pending question. A click
// that belongs to neither is dropped — an unknown custom_id is not ours to
// acknowledge.
func (s *slash) onComponent(ctx context.Context, ix dctl.Interaction) {
	if s.router == nil {
		return
	}
	value := ""
	if len(ix.Data.Values) > 0 {
		value = ix.Data.Values[0]
	}
	var msg string
	if channel, ok := ParseBindCustomID(ix.Data.CustomID); ok {
		msg = s.router.onBindPick(ctx, channel, value)
	} else if session, ok := ParseChoiceCustomID(ix.Data.CustomID); ok {
		if msg = s.router.onChoicePick(ctx, session, value); msg == "" {
			msg = "choix enregistré : " + value
		}
	} else {
		return
	}
	if s.comp != nil {
		_ = s.comp.Ack(ctx, ix.ID, ix.Token.Reveal(), msg)
	}
}

// onInteraction is the gateway's single entry point from the websocket loop. It
// routes command interactions through the registry (whose handlers respond
// themselves) and autocomplete interactions through the autocomplete dispatcher.
func (s *slash) onInteraction(ctx context.Context, ix dctl.Interaction) {
	// A component click is not a command: it answers a menu this gateway posted,
	// and the custom_id says which one. Route it before the command registry,
	// which has no handler for it.
	if ix.Type == dctl.InteractionComponent {
		s.onComponent(ctx, ix)
		return
	}
	if ix.Type == dctl.InteractionAutocomplete {
		// Gate autocomplete too: an unallowed user must not be able to enumerate
		// session names (the suggestions would otherwise leak the topology).
		if !s.allow.Allowed(ix.UserID()) {
			_ = s.ix.RespondAutocomplete(ctx, ix.ID, ix.Token.Reveal(), nil)
			return
		}
		choices, err := s.reg.DispatchAutocomplete(ctx, ix)
		if err != nil {
			return
		}
		_ = s.ix.RespondAutocomplete(ctx, ix.ID, ix.Token.Reveal(), choices)
		return
	}
	// Handlers respond internally (Defer + EditResponse, or a direct Respond), so
	// the returned Response is intentionally discarded.
	_, _ = s.reg.Dispatch(ctx, ix)
}

// --- command handlers ---

// routeAndDispatch is the plain path shared by `set` and `service` (and the
// non-allow tail of `session`): gate, resolve the command path, hand the neutral
// argv to the core seam.
func (s *slash) routeAndDispatch(ctx context.Context, ix dctl.Interaction) {
	path, leaves := route(ix.Data)
	s.dispatch(ctx, ix, append(path, flagsFrom(leaves)...))
}

func (s *slash) handleSet(ctx context.Context, ix dctl.Interaction) (dctl.Response, error) {
	if !s.gate(ctx, ix) {
		return dctl.Response{}, nil
	}
	s.routeAndDispatch(ctx, ix)
	return dctl.Response{}, nil
}

func (s *slash) handleService(ctx context.Context, ix dctl.Interaction) (dctl.Response, error) {
	if !s.gate(ctx, ix) {
		return dctl.Response{}, nil
	}
	s.routeAndDispatch(ctx, ix)
	return dctl.Response{}, nil
}

func (s *slash) handleSession(ctx context.Context, ix dctl.Interaction) (dctl.Response, error) {
	if !s.gate(ctx, ix) {
		return dctl.Response{}, nil
	}
	path, _ := route(ix.Data)
	// `/session allow …` is gateway-local policy, not a core command.
	if len(path) >= 3 && path[1] == "allow" {
		name, _ := ix.Data.Opt("name")
		user, _ := ix.Data.Opt("user")
		switch path[2] {
		case "add":
			s.respond(ctx, ix, saveNote(s.allow.AddSession(name, user), "allow store", fmt.Sprintf("added <@%s> to session %q", user, name)))
		case "remove":
			s.respond(ctx, ix, saveNote(s.allow.RemoveSession(name, user), "allow store", fmt.Sprintf("removed <@%s> from session %q", user, name)))
		case "list":
			s.respond(ctx, ix, listUsers(fmt.Sprintf("session %q", name), s.allow.ListSession(name)))
		}
		return dctl.Response{}, nil
	}
	s.routeAndDispatch(ctx, ix)
	return dctl.Response{}, nil
}

func (s *slash) handleAllow(ctx context.Context, ix dctl.Interaction) (dctl.Response, error) {
	if !s.gate(ctx, ix) {
		return dctl.Response{}, nil
	}
	path, _ := route(ix.Data)
	user, _ := ix.Data.Opt("user")
	switch lastOf(path) {
	case "add":
		s.respond(ctx, ix, saveNote(s.allow.AddGlobal(user), "allow store", fmt.Sprintf("allowed <@%s> to run commands", user)))
	case "remove":
		s.respond(ctx, ix, saveNote(s.allow.RemoveGlobal(user), "allow store", fmt.Sprintf("removed <@%s>", user)))
	case "list":
		s.respond(ctx, ix, listUsers("command allowlist", s.allow.ListGlobal()))
	}
	return dctl.Response{}, nil
}

func (s *slash) handleStop(ctx context.Context, ix dctl.Interaction) (dctl.Response, error) {
	if !s.gate(ctx, ix) {
		return dctl.Response{}, nil
	}
	s.respond(ctx, ix, s.stop(ix.ChannelID))
	return dctl.Response{}, nil
}

// stop cancels the turn running in the conversation the command was typed in.
// It takes no session name on purpose: the operator asks for it from the room
// the runaway turn is talking in, and that room already says which session it
// is. Without it the only way out of a turn gone wrong is `/session close`,
// which also throws away the worktree and everything in it.
func (s *slash) stop(channel string) string {
	if s.ctrl == nil || s.router == nil {
		return "session control is not available yet"
	}
	if !s.ctrl.Interrupt(s.router.sessionOf(s.ctrl, channel)) {
		return "aucun tour en cours ici"
	}
	return "⏹️ tour interrompu — la conversation est gardée, dis-moi la suite"
}

func (s *slash) handleVerbosity(ctx context.Context, ix dctl.Interaction) (dctl.Response, error) {
	if !s.gate(ctx, ix) {
		return dctl.Response{}, nil
	}
	level, _ := ix.Data.Opt("level")
	s.respond(ctx, ix, s.setVerbosity(ix.ChannelID, level))
	return dctl.Response{}, nil
}

// setVerbosity retunes one conversation. The level is stored per conversation
// rather than per session because it describes the room: the same operator wants
// a live tool trace in his own thread and nothing but the answer in a channel his
// team reads, and DISCORD_VERBOSITY can only say one of those, daemon-wide, until
// the next restart.
func (s *slash) setVerbosity(channel, level string) string {
	if !knownLevel(level) {
		return "niveau inconnu : " + level
	}
	if s.binds == nil {
		return "le store de conversations n'est pas disponible"
	}
	return saveNote(s.binds.SetLevel(channel, level), "bind store", "verbosité de ce salon : "+level)
}

func (s *slash) handleMode(ctx context.Context, ix dctl.Interaction) (dctl.Response, error) {
	if !s.gate(ctx, ix) {
		return dctl.Response{}, nil
	}
	mode, _ := ix.Data.Opt("mode")
	s.respond(ctx, ix, s.setMode(ix.ChannelID, mode))
	return dctl.Response{}, nil
}

// normalMode is the ordinary channel, which holds one conversation of its own.
// It is stored as no mode at all: an absent entry is what every channel written
// before /mode existed already means.
const normalMode = "normal"

// setMode puts a channel in support mode, or takes it back out. Turning support
// on also drops the channel's binding: a channel bound before the mode was set
// would keep sending every ping to that one session, and the mode would look
// like it did nothing. The session itself is left running — it may hold work,
// and `/session close` is how that is decided.
func (s *slash) setMode(channel, mode string) string {
	if s.binds == nil {
		return "le store de conversations n'est pas disponible"
	}
	switch mode {
	case supportMode:
		if err := s.binds.Unbind(channel); err != nil {
			fmt.Fprintf(os.Stderr, "discord gateway: bind store save failed: %v\n", err)
		}
		return saveNote(s.binds.SetMode(channel, supportMode), "bind store",
			"mode support : chaque ping ouvre son propre fil ici. La session déjà liée à ce salon continue de tourner — `/session close` si tu n'en veux plus.")
	case normalMode:
		return saveNote(s.binds.SetMode(channel, ""), "bind store", "mode normal : ce salon porte une seule conversation")
	}
	return "mode inconnu : " + mode
}

// autoSession suggests existing session names for any `name` option that opts
// into autocomplete (close/who/allow), filtered by what the user has typed.
func (s *slash) autoSession(ctx context.Context, ix dctl.Interaction) ([]dctl.AutocompleteChoice, error) {
	_, val, _ := ix.Data.Focused()
	val = strings.ToLower(val)
	var out []dctl.AutocompleteChoice
	for _, si := range s.sessions() {
		if val == "" || strings.Contains(strings.ToLower(si.Name), val) {
			out = append(out, dctl.AutocompleteChoice{Name: si.Name, Value: si.Name})
		}
	}
	return out, nil
}

// --- helpers ---

// gate enforces the global command allowlist, replying ephemerally when denied.
// The caller id comes from UserID(), not member.user: this bot follows its owner
// into DMs, where Discord carries the caller somewhere else entirely and a
// populated allowlist would otherwise deny him in his own DM.
func (s *slash) gate(ctx context.Context, ix dctl.Interaction) bool {
	if s.allow.Allowed(ix.UserID()) {
		return true
	}
	s.respond(ctx, ix, "you are not allowed to run commands here")
	return false
}

func (s *slash) sessions() []contracts.SessionInfo {
	if s.ctrl == nil {
		return nil
	}
	return s.ctrl.Sessions()
}

// dispatch defers the interaction (operator output is ephemeral), runs the
// neutral argv through the core SessionControl seam, then fills in the deferred
// reply with the result.
func (s *slash) dispatch(ctx context.Context, ix dctl.Interaction, args []string) {
	token := ix.Token.Reveal()
	if err := s.ix.Defer(ctx, ix.ID, token, true); err != nil {
		return
	}
	out := s.run(ctx, args)
	appID, err := s.ix.AppID(ctx)
	if err != nil {
		return
	}
	_ = s.ix.EditResponse(ctx, appID, token, dctl.Response{Content: out})
}

func (s *slash) run(ctx context.Context, args []string) string {
	if s.ctrl == nil {
		return "session control is not available yet"
	}
	out, err := s.ctrl.Dispatch(ctx, args)
	if err != nil {
		return "error: " + err.Error()
	}
	if out == "" {
		return "done"
	}
	return out
}

// respond sends an immediate ephemeral reply (used by the fast allow-list paths).
func (s *slash) respond(ctx context.Context, ix dctl.Interaction, content string) {
	_ = s.ix.Respond(ctx, ix.ID, ix.Token.Reveal(), dctl.Response{Content: content, Ephemeral: true})
}

// Discord application-command option types (subset we branch on), mirroring the
// named gateway opcodes in ws.go rather than trusting bare-int comments.
const (
	optSubCommand      = 1
	optSubCommandGroup = 2
	optBoolean         = 5
)

// saveNote returns the success message, or that message annotated with a
// not-persisted warning (and logs the cause) when the store write failed — so the
// operator is not told a change stuck when it was lost.
func saveNote(err error, store, ok string) string {
	if err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: %s save failed: %v\n", store, err)
		return ok + " (warning: not saved, will be lost on restart)"
	}
	return ok
}

// route walks the option tree, following sub-command groups and sub-commands, and
// returns the resolved command path (top-level name plus the group/sub names)
// together with the leaf options of the deepest sub.
func route(d dctl.InteractionData) (path []string, leaves []dctl.InteractionOption) {
	path = []string{d.Name}
	opts := d.Options
	for {
		var next *dctl.InteractionOption
		for i := range opts {
			if opts[i].Type == optSubCommand || opts[i].Type == optSubCommandGroup {
				next = &opts[i]
				break
			}
		}
		if next == nil {
			break
		}
		path = append(path, next.Name)
		opts = next.Options
	}
	return path, opts
}

// flagsFrom turns leaf options into the `--name value` (or bare `--name` for a
// true boolean) argv the core CLI registry parses.
func flagsFrom(leaves []dctl.InteractionOption) []string {
	var out []string
	for _, o := range leaves {
		if o.Type == optBoolean {
			if b, _ := o.Value.(bool); b {
				out = append(out, "--"+o.Name)
			}
			continue
		}
		out = append(out, "--"+o.Name, valStr(o.Value))
	}
	return out
}

func valStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}

func listUsers(label string, users []string) string {
	if len(users) == 0 {
		return label + ": empty (everyone allowed)"
	}
	mentions := make([]string, 0, len(users))
	for _, u := range users {
		mentions = append(mentions, "<@"+u+">")
	}
	return label + ": " + strings.Join(mentions, ", ")
}

func lastOf(path []string) string {
	if len(path) == 0 {
		return ""
	}
	return path[len(path)-1]
}

// --- command catalog (old dctl catalog, minus workspace, plus set home/source) ---

// Every command gates to members with Manage Server by default (Perms); the
// allow store is the finer per-user gate once populated.
func commandSet() *dctl.Command {
	return dctl.NewCommand("set", "configure the daemon").
		Perms(dctl.PermManageGuild).
		With(
			dctl.Sub("home", "set the category/forum that holds session channels",
				dctl.ChannelOpt("channel", "category or forum channel", true)),
			dctl.Sub("source", "set the source checkout `service update` builds from",
				dctl.String("path", "absolute path to the source checkout", true)),
		)
}

func commandSession() *dctl.Command {
	return dctl.NewCommand("session", "manage sessions").
		Perms(dctl.PermManageGuild).
		With(
			dctl.Sub("create", "create a session: a bridged channel + isolated worktree + backend",
				dctl.String("name", "session name", true),
				dctl.String("cmd", "bridged command (defaults to the configured cmd)", false),
				dctl.Bool("shared", "run in the main checkout instead of an isolated worktree", false),
				dctl.String("backend", "bridge backend", false).
					Choices(dctl.NewChoice("stream", "stream"), dctl.NewChoice("oneshot", "oneshot")),
				dctl.String("project", "workspace sub-dir the backend works on", false),
				dctl.String("clone", "remote repo (owner/name) to clone into the workspace first", false),
			),
			dctl.Sub("close", "close a session: stop the bridge, remove the worktree, archive the channel",
				dctl.String("name", "session name", true).Autocomplete(),
				dctl.Bool("force", "discard uncommitted worktree changes", false),
			),
			dctl.Sub("list", "list active sessions"),
			dctl.Sub("who", "list the participants observed in a session",
				dctl.String("name", "session name", true).Autocomplete()),
			dctl.Group("allow", "manage who may take part in a session",
				dctl.Sub("add", "allow a user in a session",
					dctl.String("name", "session name", true).Autocomplete(),
					dctl.User("user", "user to allow", true)),
				dctl.Sub("remove", "disallow a user from a session",
					dctl.String("name", "session name", true).Autocomplete(),
					dctl.User("user", "user to remove", true)),
				dctl.Sub("list", "list users allowed in a session",
					dctl.String("name", "session name", true).Autocomplete()),
			),
		)
}

func commandService() *dctl.Command {
	return dctl.NewCommand("service", "manage the daemon").
		Perms(dctl.PermManageGuild).
		With(
			dctl.Sub("restart", "restart the daemon"),
			dctl.Sub("update", "rebuild the daemon from source and restart it",
				dctl.Bool("no_pull", "skip the git pull before building", false)),
		)
}

// commandStop is deliberately flat and argument-free: it is typed in a panic,
// in the room the runaway turn is in.
func commandStop() *dctl.Command {
	return dctl.NewCommand("stop", "cancel the turn running in this conversation").
		Perms(dctl.PermManageGuild)
}

func commandVerbosity() *dctl.Command {
	return dctl.NewCommand("verbosity", "set how much of a turn this conversation shows").
		Perms(dctl.PermManageGuild).
		With(
			dctl.String("level", "how much of a turn to show here", true).
				Choices(
					dctl.NewChoice("silent — the answer and nothing else", levelSilent),
					dctl.NewChoice("quiet — adds a live list of tool names", levelQuiet),
					dctl.NewChoice("actions — adds each tool's detail", levelActions),
					dctl.NewChoice("full — adds the assistant's text", levelFull),
				),
		)
}

func commandMode() *dctl.Command {
	return dctl.NewCommand("mode", "set how this channel handles pings").
		Perms(dctl.PermManageGuild).
		With(
			dctl.String("mode", "how pings are handled here", true).
				Choices(
					dctl.NewChoice("support — every ping opens its own thread", supportMode),
					dctl.NewChoice("normal — this channel holds one conversation", normalMode),
				),
		)
}

func commandAllow() *dctl.Command {
	return dctl.NewCommand("allow", "manage who may run commands").
		Perms(dctl.PermManageGuild).
		With(
			dctl.Sub("add", "allow a user to run commands", dctl.User("user", "user to allow", true)),
			dctl.Sub("remove", "remove a user from the command allowlist", dctl.User("user", "user to remove", true)),
			dctl.Sub("list", "list users allowed to run commands"),
		)
}
