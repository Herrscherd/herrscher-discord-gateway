package discord

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

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
	ctx   context.Context
	token string
	ix    *dctl.Interactions
	reg   *dctl.Registry
	allow *allowStore
	ctrl  contracts.SessionControl
}

// newSlash builds the slash runtime and registers the command catalog + handlers
// on the interactions registry. ctrl is bound later (BindSessionControl) once the
// daemon hands the gateway its runtime session controller.
func newSlash(ctx context.Context, ix *dctl.Interactions, token string, allow *allowStore) *slash {
	s := &slash{ctx: ctx, token: token, ix: ix, reg: ix.Registry(), allow: allow}
	s.reg.
		Add(commandSet(), s.handleSet).
		Add(commandSession(), s.handleSession).
		Add(commandService(), s.handleService).
		Add(commandAllow(), s.handleAllow).
		Autocomplete("session", s.autoSession)
	return s
}

// start syncs the command catalog to Discord and runs the gateway websocket loop
// until the daemon context is cancelled. It is called once the session
// controller is bound, so dispatch always has a live ctrl.
func (s *slash) start() {
	if err := s.reg.Sync(s.ctx); err != nil {
		fmt.Fprintf(os.Stderr, "discord gateway: command sync: %v\n", err)
	}
	newWS(s.token, s.onInteraction).run(s.ctx)
}

// onInteraction is the gateway's single entry point from the websocket loop. It
// routes command interactions through the registry (whose handlers respond
// themselves) and autocomplete interactions through the autocomplete dispatcher.
func (s *slash) onInteraction(ctx context.Context, ix dctl.Interaction) {
	if ix.Type == dctl.InteractionAutocomplete {
		// Gate autocomplete too: an unallowed user must not be able to enumerate
		// session names (the suggestions would otherwise leak the topology).
		if !s.allow.Allowed(ix.Member.User.ID) {
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

func (s *slash) handleSet(ctx context.Context, ix dctl.Interaction) (dctl.Response, error) {
	if !s.gate(ctx, ix) {
		return dctl.Response{}, nil
	}
	path, leaves := route(ix.Data)
	s.dispatch(ctx, ix, append(path, flagsFrom(leaves)...))
	return dctl.Response{}, nil
}

func (s *slash) handleService(ctx context.Context, ix dctl.Interaction) (dctl.Response, error) {
	if !s.gate(ctx, ix) {
		return dctl.Response{}, nil
	}
	path, leaves := route(ix.Data)
	s.dispatch(ctx, ix, append(path, flagsFrom(leaves)...))
	return dctl.Response{}, nil
}

func (s *slash) handleSession(ctx context.Context, ix dctl.Interaction) (dctl.Response, error) {
	if !s.gate(ctx, ix) {
		return dctl.Response{}, nil
	}
	path, leaves := route(ix.Data)
	// `/session allow …` is gateway-local policy, not a core command.
	if len(path) >= 3 && path[1] == "allow" {
		name, _ := ix.Data.Opt("name")
		user, _ := ix.Data.Opt("user")
		switch path[2] {
		case "add":
			_ = s.allow.AddSession(name, user)
			s.respond(ctx, ix, fmt.Sprintf("added <@%s> to session %q", user, name))
		case "remove":
			_ = s.allow.RemoveSession(name, user)
			s.respond(ctx, ix, fmt.Sprintf("removed <@%s> from session %q", user, name))
		case "list":
			s.respond(ctx, ix, listUsers(fmt.Sprintf("session %q", name), s.allow.ListSession(name)))
		}
		return dctl.Response{}, nil
	}
	s.dispatch(ctx, ix, append(path, flagsFrom(leaves)...))
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
		_ = s.allow.AddGlobal(user)
		s.respond(ctx, ix, fmt.Sprintf("allowed <@%s> to run commands", user))
	case "remove":
		_ = s.allow.RemoveGlobal(user)
		s.respond(ctx, ix, fmt.Sprintf("removed <@%s>", user))
	case "list":
		s.respond(ctx, ix, listUsers("command allowlist", s.allow.ListGlobal()))
	}
	return dctl.Response{}, nil
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
func (s *slash) gate(ctx context.Context, ix dctl.Interaction) bool {
	if s.allow.Allowed(ix.Member.User.ID) {
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

// route walks the option tree, following sub-command groups (type 2) and
// sub-commands (type 1), and returns the resolved command path (top-level name
// plus the group/sub names) together with the leaf options of the deepest sub.
func route(d dctl.InteractionData) (path []string, leaves []dctl.InteractionOption) {
	path = []string{d.Name}
	opts := d.Options
	for {
		var next *dctl.InteractionOption
		for i := range opts {
			if opts[i].Type == 1 || opts[i].Type == 2 { // SUB_COMMAND | SUB_COMMAND_GROUP
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
		if o.Type == 5 { // BOOLEAN
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

func commandAllow() *dctl.Command {
	return dctl.NewCommand("allow", "manage who may run commands").
		Perms(dctl.PermManageGuild).
		With(
			dctl.Sub("add", "allow a user to run commands", dctl.User("user", "user to allow", true)),
			dctl.Sub("remove", "remove a user from the command allowlist", dctl.User("user", "user to remove", true)),
			dctl.Sub("list", "list users allowed to run commands"),
		)
}
