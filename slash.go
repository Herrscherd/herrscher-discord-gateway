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
	msgs   messages
}

type acker interface {
	Ack(ctx context.Context, id, token, content string) error
}

func newSlash(ix *dctl.Interactions, comp acker, gatewayURL, token string, allow *allowStore, binds *bindStore, msgs messages, log *slog.Logger) *slash {
	handler := &slash{log: log, ix: ix, comp: comp, reg: ix.Registry(), allow: allow, binds: binds, msgs: msgs}
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
		s.ackComponent(ctx, interaction, s.msgs.say("menu.not_allowed", nil))
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
	return s.msgs.say("choice.saved", map[string]any{"Value": value}), true
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
			s.respond(ctx, interaction, s.saveNote(s.allow.AddSession(name, user), "allow store", s.msgs.say("allow.session_added", map[string]any{mentionArg: mention(user), nameArg: name})))
		case "remove":
			s.respond(ctx, interaction, s.saveNote(s.allow.RemoveSession(name, user), "allow store", s.msgs.say("allow.session_removed", map[string]any{mentionArg: mention(user), nameArg: name})))
		case "list":
			s.respond(ctx, interaction, listUsers(s.msgs, s.msgs.say("allow.label_session", map[string]any{nameArg: name}), s.allow.ListSession(name)))
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
		s.respond(ctx, interaction, s.saveNote(s.allow.AddGlobal(user), "allow store", s.msgs.say("allow.global_added", map[string]any{mentionArg: mention(user)})))
	case "remove":
		s.respond(ctx, interaction, s.saveNote(s.allow.RemoveGlobal(user), "allow store", s.msgs.say("allow.global_removed", map[string]any{mentionArg: mention(user)})))
	case "list":
		s.respond(ctx, interaction, listUsers(s.msgs, s.msgs.say("allow.label_global", nil), s.allow.ListGlobal()))
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
		return s.msgs.say("control.unavailable", nil)
	}
	if !s.ctrl.Interrupt(s.router.sessionOf(s.ctrl, channel, user, direct)) {
		return s.msgs.say("stop.none", nil)
	}
	return "⏹️ " + s.msgs.say("stop.done", nil)
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
		return s.msgs.say("verbosity.unknown", map[string]any{"Level": level})
	}
	if s.binds == nil {
		return s.msgs.say("store.unavailable", nil)
	}
	return s.saveNote(s.binds.SetLevel(channel, level), "bind store", s.msgs.say("verbosity.set", map[string]any{"Level": level}))
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
		return s.msgs.say("store.unavailable", nil)
	}
	if !knownMode(mode) {
		return s.msgs.say("mode.unknown", map[string]any{"Mode": mode})
	}
	if mode == ambientMode && s.agent() == "" {
		return s.msgs.say("mode.ambient_refused", nil)
	}
	if mode != normalMode {
		if err := s.binds.UnbindAll(channel); err != nil {
			s.log.Warn("bind store save failed", "err", err)
		}
	}
	switch mode {
	case supportMode:
		return s.saveNote(s.binds.SetMode(channel, supportMode), "bind store", s.msgs.say("mode.support", nil))
	case ambientMode:
		return s.saveNote(s.binds.SetMode(channel, ambientMode), "bind store", s.msgs.say("mode.ambient", nil))
	case offMode:
		return s.saveNote(s.binds.SetMode(channel, offMode), "bind store", s.msgs.say("mode.off", nil))
	}
	return s.saveNote(s.binds.SetMode(channel, ""), "bind store", s.msgs.say("mode.normal", nil))
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
	s.respond(ctx, interaction, s.msgs.say("gate.denied", nil))
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
		return s.msgs.say("control.unavailable", nil)
	}
	result, err := s.ctrl.Dispatch(ctx, args)
	if err != nil {
		return s.msgs.noticeText(s.msgs.lang, contracts.NoticeOf(err))
	}
	if notice, ok := result.Notice(); ok {
		return s.msgs.noticeText(s.msgs.lang, notice)
	}
	return s.dataText(result.Data())
}

func (s *slash) dataText(data any) string {
	if data == nil {
		return s.msgs.say("run.done", nil)
	}
	text := fmt.Sprint(data)
	if text == "" {
		return s.msgs.say("run.done", nil)
	}
	return text
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
		return ok + " " + s.msgs.say("save.unreadable", nil)
	}
	return ok + " " + s.msgs.say("save.lost", nil)
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

func listUsers(texts messages, label string, users []string) string {
	if len(users) == 0 {
		return texts.say("allow.empty", map[string]any{"Label": label})
	}
	mentions := make([]string, 0, len(users))
	for _, u := range users {
		mentions = append(mentions, mention(u))
	}
	return texts.say("allow.list", map[string]any{"Label": label, "Mentions": strings.Join(mentions, ", ")})
}

func mention(userID string) string {
	return "<@" + userID + ">"
}

func lastOf(path []string) string {
	if len(path) == 0 {
		return ""
	}
	return path[len(path)-1]
}
