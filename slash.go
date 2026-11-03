package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

type slash struct {
	log    *slog.Logger
	token  string
	ix     *dctl.Interactions
	reg    *dctl.Registry
	allow  *allowStore
	binds  *bindStore
	ctrl   contracts.SessionControl
	comp   acker
	router *router

	gateway string
}

type acker interface {
	Ack(ctx context.Context, id, token, content string) error
}

func newSlash(ix *dctl.Interactions, comp acker, token string, allow *allowStore, binds *bindStore, log *slog.Logger) *slash {
	handler := &slash{log: log, token: token, ix: ix, comp: comp, reg: ix.Registry(), allow: allow, binds: binds}
	handler.reg.
		Add(commandSet(), handler.handleSet).
		Add(commandSession(), handler.handleSession).
		Add(commandService(), handler.handleService).
		Add(commandAllow(), handler.handleAllow).
		Add(commandStop(), handler.handleStop).
		Add(commandVerbosity(), handler.handleVerbosity).
		Add(commandMode(), handler.handleMode).
		Autocomplete("session", handler.autoSession)
	return handler
}

func (s *slash) start(ctx context.Context) {
	syncDone := make(chan struct{})
	go func() {
		defer close(syncDone)
		s.sync(ctx)
	}()
	gateway := newWS(s.token, s.log, s.onInteraction, s.onMessage, s.onGone)
	if s.gateway != "" {
		gateway.url = s.gateway
	}
	gateway.run(ctx)
	<-syncDone
}

func syncBackoffs() []time.Duration {
	return []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute}
}

func (s *slash) sync(ctx context.Context) { syncWithRetry(ctx, s.reg.Sync, s.log, syncBackoffs()) }

func syncWithRetry(ctx context.Context, sync func(context.Context) error, log *slog.Logger, backoff []time.Duration) {
	for attempt := 0; ; attempt++ {
		err := sync(ctx)
		if err == nil {
			return
		}
		if attempt == len(backoff) {
			log.Error("command sync gave up", "attempts", attempt+1, "err", err)
			return
		}
		log.Warn("command sync failed, retrying", "err", err, "retry_in", backoff[attempt].String())
		select {
		case <-time.After(backoff[attempt]):
		case <-ctx.Done():
			return
		}
	}
}

func (s *slash) onMessage(ctx context.Context, msg messageCreate) {
	if s.router != nil {
		s.router.onMessage(ctx, msg)
	}
}

func (s *slash) onGone(ctx context.Context, channelID string) {
	if s.router != nil {
		s.router.onChannelGone(ctx, channelID)
	}
}

func (s *slash) onComponent(ctx context.Context, interaction dctl.Interaction) {
	if s.router == nil {
		return
	}
	if !s.allow.Allowed(interaction.UserID()) {
		s.ackComponent(ctx, interaction, "tu n'es pas autorisé à utiliser ce menu")
		return
	}
	msg, ok := s.componentReply(ctx, interaction, firstValue(interaction.Data.Values))
	if !ok {
		return
	}
	s.ackComponent(ctx, interaction, msg)
}

func (s *slash) componentReply(ctx context.Context, interaction dctl.Interaction, value string) (string, bool) {
	if channel, ok := ParseBindCustomID(interaction.Data.CustomID); ok {
		return s.router.onBindPick(ctx, channel, interaction.UserID(), value), true
	}
	session, ok := ParseChoiceCustomID(interaction.Data.CustomID)
	if !ok {
		return "", false
	}
	if msg := s.router.onChoicePick(ctx, session, interaction.UserID(), interaction.GuildID == "", value); msg != "" {
		return msg, true
	}
	return "choix enregistré : " + value, true
}

func (s *slash) ackComponent(ctx context.Context, interaction dctl.Interaction, msg string) {
	if s.comp == nil {
		return
	}
	if err := s.comp.Ack(ctx, interaction.ID, interaction.Token.Reveal(), msg); err != nil {
		s.log.Warn("acknowledge component failed", "err", err)
	}
}

func firstValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (s *slash) onInteraction(ctx context.Context, interaction dctl.Interaction) {
	ctx = contracts.WithPrincipal(ctx, principalOf(interaction))
	if interaction.Type == dctl.InteractionComponent {
		s.onComponent(ctx, interaction)
		return
	}
	if interaction.Type == dctl.InteractionAutocomplete {
		if !s.allow.Allowed(interaction.UserID()) {
			if err := s.ix.RespondAutocomplete(ctx, interaction.ID, interaction.Token.Reveal(), nil); err != nil {
				s.log.Warn("answer autocomplete failed", "err", err)
			}
			return
		}
		choices, err := s.reg.DispatchAutocomplete(ctx, interaction)
		if err != nil {
			s.log.Warn("autocomplete failed", "err", err)
			return
		}
		if err := s.ix.RespondAutocomplete(ctx, interaction.ID, interaction.Token.Reveal(), choices); err != nil {
			s.log.Warn("answer autocomplete failed", "err", err)
		}
		return
	}
	if _, err := s.reg.Dispatch(ctx, interaction); err != nil {
		s.log.Warn("dispatch interaction failed", "err", err)
	}
}

func (s *slash) routeAndDispatch(ctx context.Context, interaction dctl.Interaction) {
	path, leaves := route(interaction.Data)
	s.dispatch(ctx, interaction, append(path, flagsFrom(leaves)...))
}

func (s *slash) handleSet(ctx context.Context, interaction dctl.Interaction) (dctl.Response, error) {
	if !s.gate(ctx, interaction) {
		return dctl.Response{}, nil
	}
	s.routeAndDispatch(ctx, interaction)
	return dctl.Response{}, nil
}

func (s *slash) handleService(ctx context.Context, interaction dctl.Interaction) (dctl.Response, error) {
	if !s.gate(ctx, interaction) {
		return dctl.Response{}, nil
	}
	s.routeAndDispatch(ctx, interaction)
	return dctl.Response{}, nil
}

func (s *slash) handleSession(ctx context.Context, interaction dctl.Interaction) (dctl.Response, error) {
	if !s.gate(ctx, interaction) {
		return dctl.Response{}, nil
	}
	path, _ := route(interaction.Data)
	if len(path) >= 3 && path[1] == "allow" {
		name, _ := interaction.Data.Opt("name")
		user, _ := interaction.Data.Opt("user")
		switch path[2] {
		case "add":
			s.respond(ctx, interaction, s.saveNote(s.allow.AddSession(name, user), "allow store", fmt.Sprintf("ajouté <@%s> à la session %q", user, name)))
		case "remove":
			s.respond(ctx, interaction, s.saveNote(s.allow.RemoveSession(name, user), "allow store", fmt.Sprintf("retiré <@%s> de la session %q", user, name)))
		case "list":
			s.respond(ctx, interaction, listUsers(fmt.Sprintf("session %q", name), s.allow.ListSession(name)))
		}
		return dctl.Response{}, nil
	}
	s.routeAndDispatch(ctx, interaction)
	return dctl.Response{}, nil
}

func (s *slash) handleAllow(ctx context.Context, interaction dctl.Interaction) (dctl.Response, error) {
	if !s.gate(ctx, interaction) {
		return dctl.Response{}, nil
	}
	path, _ := route(interaction.Data)
	user, _ := interaction.Data.Opt("user")
	switch lastOf(path) {
	case "add":
		s.respond(ctx, interaction, s.saveNote(s.allow.AddGlobal(user), "allow store", fmt.Sprintf("<@%s> est autorisé à utiliser le bot", user)))
	case "remove":
		s.respond(ctx, interaction, s.saveNote(s.allow.RemoveGlobal(user), "allow store", fmt.Sprintf("<@%s> retiré de la liste", user)))
	case "list":
		s.respond(ctx, interaction, listUsers("command allowlist", s.allow.ListGlobal()))
	}
	return dctl.Response{}, nil
}

func (s *slash) handleStop(ctx context.Context, interaction dctl.Interaction) (dctl.Response, error) {
	if !s.gate(ctx, interaction) {
		return dctl.Response{}, nil
	}
	s.respond(ctx, interaction, s.stop(interaction.ChannelID, interaction.UserID(), interaction.GuildID == ""))
	return dctl.Response{}, nil
}

func (s *slash) stop(channel, user string, direct bool) string {
	if s.ctrl == nil || s.router == nil {
		return "le contrôle des sessions n'est pas encore disponible"
	}
	if !s.ctrl.Interrupt(s.router.sessionOf(s.ctrl, channel, user, direct)) {
		return "aucun tour en cours ici"
	}
	return "⏹️ tour interrompu, la conversation est gardée, dis-moi la suite"
}

func (s *slash) handleVerbosity(ctx context.Context, interaction dctl.Interaction) (dctl.Response, error) {
	if !s.gate(ctx, interaction) {
		return dctl.Response{}, nil
	}
	level, _ := interaction.Data.Opt("level")
	s.respond(ctx, interaction, s.setVerbosity(interaction.ChannelID, level))
	return dctl.Response{}, nil
}

func (s *slash) setVerbosity(channel, level string) string {
	if !knownLevel(level) {
		return "niveau inconnu : " + level
	}
	if s.binds == nil {
		return "le store de conversations n'est pas disponible"
	}
	return s.saveNote(s.binds.SetLevel(channel, level), "bind store", "verbosité de ce salon : "+level)
}

func (s *slash) handleMode(ctx context.Context, interaction dctl.Interaction) (dctl.Response, error) {
	if !s.gate(ctx, interaction) {
		return dctl.Response{}, nil
	}
	mode, _ := interaction.Data.Opt("mode")
	s.respond(ctx, interaction, s.setMode(interaction.ChannelID, mode))
	return dctl.Response{}, nil
}

func (s *slash) setMode(channel, mode string) string {
	if s.binds == nil {
		return "le store de conversations n'est pas disponible"
	}
	if !knownMode(mode) {
		return "mode inconnu : " + mode
	}
	if mode == ambientMode && s.agent() == "" {
		return "mode ambient refusé : il fait répondre le bot à tout le monde sans qu'on l'appelle, et rien ne borne ce qu'il peut faire tant qu'aucune persona n'est configurée. Renseigne DISCORD_AGENT puis réessaie."
	}
	if mode != normalMode {
		if err := s.binds.UnbindAll(channel); err != nil {
			s.log.Warn("bind store save failed", "err", err)
		}
	}
	switch mode {
	case supportMode:
		return s.saveNote(s.binds.SetMode(channel, supportMode), "bind store",
			"mode support : chaque ping ouvre son propre fil ici. Les sessions déjà liées à ce salon tournent toujours, `/session close` si tu n'en veux plus.")
	case ambientMode:
		return s.saveNote(s.binds.SetMode(channel, ambientMode), "bind store",
			"mode ambient : je réponds ici sans qu'on m'appelle, à qui a le droit de me faire agir. Les sessions déjà liées à ce salon tournent toujours, `/session close` si tu n'en veux plus.")
	case offMode:
		return s.saveNote(s.binds.SetMode(channel, offMode), "bind store",
			"mode off : je ne réponds plus rien ici, même appelé. Les sessions déjà liées à ce salon tournent toujours, `/session close` si tu n'en veux plus.")
	}
	return s.saveNote(s.binds.SetMode(channel, ""), "bind store", "mode normal : il faut m'appeler ici, et chacun repart sur sa propre session")
}

func (s *slash) agent() string {
	if s.router == nil {
		return ""
	}
	return s.router.cfg.agent
}

func (s *slash) autoSession(_ context.Context, ix dctl.Interaction) ([]dctl.AutocompleteChoice, error) {
	_, val, _ := ix.Data.Focused()
	val = strings.ToLower(val)
	var out []dctl.AutocompleteChoice
	items := s.sessions()
	for i := range items {
		item := items[i]
		if val == "" || strings.Contains(strings.ToLower(item.Name), val) {
			out = append(out, dctl.AutocompleteChoice{Name: item.Name, Value: item.Name})
		}
	}
	return out, nil
}

func (s *slash) gate(ctx context.Context, interaction dctl.Interaction) bool {
	if s.allow.Allowed(interaction.UserID()) {
		return true
	}
	s.respond(ctx, interaction, "you are not allowed to run commands here")
	return false
}

func (s *slash) sessions() []contracts.SessionInfo {
	if s.ctrl == nil {
		return nil
	}
	return s.ctrl.Sessions()
}

func (s *slash) dispatch(ctx context.Context, interaction dctl.Interaction, args []string) {
	token := interaction.Token.Reveal()
	if err := s.ix.Defer(ctx, interaction.ID, token, true); err != nil {
		s.log.Warn("defer interaction failed", "err", err)
		return
	}
	out := s.run(ctx, args)
	appID, err := s.ix.AppID(ctx)
	if err != nil {
		s.log.Warn("resolve application id for the response failed", "err", err)
		return
	}
	if err := s.ix.EditResponse(ctx, appID, token, dctl.Response{Content: out}); err != nil {
		s.log.Warn("edit interaction response failed", "err", err)
	}
}

func (s *slash) run(ctx context.Context, args []string) string {
	if s.ctrl == nil {
		return "le contrôle des sessions n'est pas encore disponible"
	}
	out, err := s.ctrl.Dispatch(ctx, args)
	if err != nil {
		return "erreur : " + err.Error()
	}
	if out == "" {
		return "fait"
	}
	return out
}

func (s *slash) respond(ctx context.Context, ix dctl.Interaction, content string) {
	if err := s.ix.Respond(ctx, ix.ID, ix.Token.Reveal(), dctl.Response{Content: content, Ephemeral: true}); err != nil {
		s.log.Warn("respond to interaction failed", "err", err)
	}
}

const (
	optSubCommand      = 1
	optSubCommandGroup = 2
	optBoolean         = 5
)

func (s *slash) saveNote(err error, store, ok string) string {
	if err != nil {
		s.log.Warn("store save failed", "store", store, "err", err)
		return ok + " (attention : non sauvegardé, perdu au prochain lancement)"
	}
	return ok
}

func route(data dctl.InteractionData) ([]string, []dctl.InteractionOption) {
	path := []string{data.Name}
	opts := data.Options
	for {
		var next *dctl.InteractionOption
		for index := range opts {
			if opts[index].Type == optSubCommand || opts[index].Type == optSubCommandGroup {
				next = &opts[index]
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

func flagsFrom(leaves []dctl.InteractionOption) []string {
	var out []string
	for _, leaf := range leaves {
		if leaf.Type == optBoolean {
			if b, ok := leaf.Value.(bool); ok && b {
				out = append(out, "--"+leaf.Name)
			}
			continue
		}
		out = append(out, "--"+leaf.Name, valStr(leaf.Value))
	}
	return out
}

func valStr(v any) string {
	switch typed := v.(type) {
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return fmt.Sprint(typed)
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
					dctl.NewChoice("silent: only the answer", levelSilent),
					dctl.NewChoice("quiet: also a live list of tool names", levelQuiet),
					dctl.NewChoice("actions: also each tool's detail", levelActions),
					dctl.NewChoice("full: also the assistant's text", levelFull),
				),
		)
}

func commandMode() *dctl.Command {
	return dctl.NewCommand("mode", "set how this channel handles pings").
		Perms(dctl.PermManageGuild).
		With(
			dctl.String("mode", "how pings are handled here", true).
				Choices(
					dctl.NewChoice("normal: the bot answers when it is mentioned", normalMode),
					dctl.NewChoice("support: every ping opens its own thread", supportMode),
					dctl.NewChoice("ambient: the bot answers here without being mentioned", ambientMode),
					dctl.NewChoice("off: the bot ignores this channel entirely", offMode),
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
