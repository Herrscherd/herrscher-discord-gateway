package discord

import (
	"context"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

type voiceRoute struct {
	agent    string
	reply    contracts.ReplyRule
	channels idSet
	own      bool
	appID    string
}

func (r *router) agentVoices(ctrl contracts.SessionControl) []contracts.AgentVoice {
	directory, ok := ctrl.(contracts.AgentDirectory)
	if !ok {
		return nil
	}
	return directory.AgentVoices(gatewayKind)
}

func (r *router) voiceRoutes(ctrl contracts.SessionControl) []voiceRoute {
	var routes []voiceRoute
	for _, voice := range r.agentVoices(ctrl) {
		if !validAgent(voice.Agent) {
			continue
		}
		bot, own := r.cfg.voices.ownOf(voice.Agent)
		routes = append(routes, voiceRoute{
			agent:    voice.Agent,
			reply:    voice.Reply,
			channels: channelSet(voice.Channels),
			own:      own,
			appID:    bot.appID,
		})
	}
	return routes
}

func channelSet(ids []string) idSet {
	set := make(idSet, len(ids))
	for _, channelID := range ids {
		if channelID != "" {
			set[channelID] = true
		}
	}
	return set
}

func (r *router) routeOf(ctrl contracts.SessionControl, agent string) (voiceRoute, bool) {
	for _, route := range r.voiceRoutes(ctrl) {
		if route.agent == agent {
			return route, true
		}
	}
	return voiceRoute{}, false
}

func (r *router) addressesAgentBot(msg messageCreate) bool {
	for _, appID := range r.cfg.voices.agentAppIDs() {
		if mentionsID(msg, appID) {
			return !r.cfg.address.mentioned(msg)
		}
	}
	return false
}

func (r *router) houseVoice(ctrl contracts.SessionControl, msg messageCreate, parent string) (voiceRoute, bool) {
	for _, route := range r.voiceRoutes(ctrl) {
		if route.own || route.reply == contracts.ReplyNever {
			continue
		}
		if mentionsAgentText(msg.Content, route.agent) {
			return route, true
		}
		if route.reply == contracts.ReplyInChannels && route.channels.matches(msg.ChannelID, parent) {
			return route, true
		}
	}
	return voiceRoute{}, false
}

func (r *router) onAgentMessage(ctx context.Context, agent string, msg messageCreate) {
	ctrl := r.ctrl()
	if ctrl == nil {
		return
	}
	route, ok := r.routeOf(ctrl, agent)
	if !ok || route.reply == contracts.ReplyNever {
		return
	}
	parent := r.binds.Parent(msg.ChannelID)
	if !r.voiceAddressed(route, msg, parent) {
		return
	}
	r.answerAs(ctx, ctrl, route, msg)
}

func (r *router) voiceAddressed(route voiceRoute, msg messageCreate, parent string) bool {
	if msg.direct() || mentionsID(msg, route.appID) {
		return true
	}
	if (addressPolicy{appID: route.appID}).repliesToSelf(msg) {
		return true
	}
	return route.reply == contracts.ReplyInChannels && route.channels.matches(msg.ChannelID, parent)
}

func (r *router) mentionedBy(route voiceRoute, msg messageCreate) bool {
	if route.own {
		return mentionsID(msg, route.appID)
	}
	return mentionsAgentText(msg.Content, route.agent)
}

func (r *router) answerAs(ctx context.Context, ctrl contracts.SessionControl, route voiceRoute, msg messageCreate) {
	bare := msg.ChannelID
	parent := r.binds.Parent(bare)
	if r.mode(bare, parent) == offMode {
		return
	}
	if !r.acl.permits(msg, parent, r.mentionedBy(route, msg)) {
		return
	}
	conv := voiced(bare, route.agent)
	msg.ChannelID = conv
	msg = r.hydrate(ctx, msg)
	chatType := r.chatType(conv, msg.direct())
	slot := r.cfg.scope.slot(chatType, conv, msg.Author.ID)
	shared := r.cfg.scope.multiUser(chatType)
	if session := r.binds.Session(slot); session != "" {
		if r.submit(ctx, ctrl, session, conv, msg, false, shared) {
			return
		}
		if err := r.binds.Unbind(slot); err != nil {
			r.noteSaveFailure(ctx, conv, err)
		}
	}
	task := job{conv: conv, parent: bare, slot: slot, shared: shared, agent: route.agent}
	r.askPlaced(ctx, ctrl, msg, func() job { return task })
}
