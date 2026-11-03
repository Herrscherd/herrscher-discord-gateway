package discord

import (
	"context"
	"errors"
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
	ix     *dctl.Interactions
	reg    *dctl.Registry
	allow  *allowStore
	binds  *bindStore
	ctrl   contracts.SessionControl
	comp   acker
	router *router
	socket *ws
}

type acker interface {
	Ack(ctx context.Context, id, token, content string) error
}

func newSlash(ix *dctl.Interactions, comp acker, gatewayURL, token string, allow *allowStore, binds *bindStore, log *slog.Logger) *slash {
	handler := &slash{log: log, ix: ix, comp: comp, reg: ix.Registry(), allow: allow, binds: binds}
	handler.socket = newWS(gatewayURL, token, log, handler.onInteraction, handler.onMessage, handler.onGone)
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
	s.socket.run(ctx)
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
	s.respond(ctx, interaction, "tu n'es pas autorisé à utiliser le bot ici")
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
	if err == nil {
		return ok
	}
	s.log.Warn("store save failed", "store", store, "err", err)
	if errors.Is(err, errStoreLocked) {
		return ok + " (attention : non sauvegardé, le fichier est illisible, aucun changement ne sera retenu tant qu'il n'est pas réparé)"
	}
	return ok + " (attention : non sauvegardé, perdu au prochain lancement)"
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
	return dctl.NewCommand("set", "configurer le démon").
		Perms(dctl.PermManageGuild).
		With(
			dctl.Sub("home", "définir la catégorie ou le forum qui accueille les salons de session",
				dctl.ChannelOpt("channel", "salon de catégorie ou de forum", true)),
			dctl.Sub("source", "définir le dépôt source à partir duquel `service update` compile",
				dctl.String("path", "chemin absolu du dépôt source", true)),
		)
}

func commandSession() *dctl.Command {
	return dctl.NewCommand("session", "gérer les sessions").
		Perms(dctl.PermManageGuild).
		With(
			dctl.Sub("create", "créer une session : un salon relié, un worktree isolé et un backend",
				dctl.String("name", "nom de la session", true),
				dctl.String("cmd", "commande reliée (par défaut la commande configurée)", false),
				dctl.Bool("shared", "lancer dans le dépôt principal au lieu d'un worktree isolé", false),
				dctl.String("backend", "backend du pont", false).
					Choices(dctl.NewChoice("stream", "stream"), dctl.NewChoice("oneshot", "oneshot")),
				dctl.String("project", "sous-dossier de l'espace de travail où travaille le backend", false),
				dctl.String("clone", "dépôt distant (propriétaire/nom) à cloner d'abord dans l'espace de travail", false),
			),
			dctl.Sub("close", "fermer une session : arrêter le pont, supprimer le worktree, archiver le salon",
				dctl.String("name", "nom de la session", true).Autocomplete(),
				dctl.Bool("force", "abandonner les modifications non commitées du worktree", false),
			),
			dctl.Sub("list", "lister les sessions actives"),
			dctl.Sub("who", "lister les participants observés dans une session",
				dctl.String("name", "nom de la session", true).Autocomplete()),
			dctl.Group("allow", "gérer qui peut participer à une session",
				dctl.Sub("add", "autoriser un utilisateur dans une session",
					dctl.String("name", "nom de la session", true).Autocomplete(),
					dctl.User("user", "utilisateur à autoriser", true)),
				dctl.Sub("remove", "retirer l'autorisation d'un utilisateur dans une session",
					dctl.String("name", "nom de la session", true).Autocomplete(),
					dctl.User("user", "utilisateur à retirer", true)),
				dctl.Sub("list", "lister les utilisateurs autorisés dans une session",
					dctl.String("name", "nom de la session", true).Autocomplete()),
			),
		)
}

func commandService() *dctl.Command {
	return dctl.NewCommand("service", "gérer le démon").
		Perms(dctl.PermManageGuild).
		With(
			dctl.Sub("restart", "redémarrer le démon"),
			dctl.Sub("update", "recompiler le démon depuis les sources et le redémarrer",
				dctl.Bool("no_pull", "ne pas faire de git pull avant la compilation", false)),
		)
}

func commandStop() *dctl.Command {
	return dctl.NewCommand("stop", "annuler le tour en cours dans cette conversation").
		Perms(dctl.PermManageGuild)
}

func commandVerbosity() *dctl.Command {
	return dctl.NewCommand("verbosity", "régler ce que cette conversation affiche d'un tour").
		Perms(dctl.PermManageGuild).
		With(
			dctl.String("level", "ce qu'il faut afficher d'un tour ici", true).
				Choices(
					dctl.NewChoice("silencieux : seulement la réponse", levelSilent),
					dctl.NewChoice("discret : aussi la liste en direct des noms d'outils", levelQuiet),
					dctl.NewChoice("actions : aussi le détail de chaque outil", levelActions),
					dctl.NewChoice("complet : aussi le texte de l'assistant", levelFull),
				),
		)
}

func commandMode() *dctl.Command {
	return dctl.NewCommand("mode", "régler comment ce salon traite les mentions").
		Perms(dctl.PermManageGuild).
		With(
			dctl.String("mode", "comment les mentions sont traitées ici", true).
				Choices(
					dctl.NewChoice("normal : le bot répond quand il est mentionné", normalMode),
					dctl.NewChoice("support : chaque mention ouvre son propre fil", supportMode),
					dctl.NewChoice("ambiant : le bot répond ici sans être mentionné", ambientMode),
					dctl.NewChoice("désactivé : le bot ignore entièrement ce salon", offMode),
				),
		)
}

func commandAllow() *dctl.Command {
	return dctl.NewCommand("allow", "gérer qui peut utiliser le bot").
		Perms(dctl.PermManageGuild).
		With(
			dctl.Sub("add", "autoriser un utilisateur à utiliser le bot", dctl.User("user", "utilisateur à autoriser", true)),
			dctl.Sub("remove", "retirer un utilisateur de la liste d'autorisation", dctl.User("user", "utilisateur à retirer", true)),
			dctl.Sub("list", "lister les utilisateurs autorisés à utiliser le bot"),
		)
}
