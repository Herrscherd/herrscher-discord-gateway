package discord

import (
	"context"
	"slices"
	"sync"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

type voiceRoute struct {
	agent    string
	reply    contracts.ReplyRule
	channels idSet
	mention  agentMention
	own      bool
	pending  bool
	appID    string
}

func (v voiceRoute) answersThroughHouse() bool {
	return !v.own && !v.pending && v.reply != contracts.ReplyNever
}

func (v voiceRoute) mayMatterBlank(msg messageCreate, parent string) bool {
	if v.reply == contracts.ReplyNever {
		return false
	}
	if !v.own || v.pending {
		return true
	}
	return mentionsID(msg, v.appID) || (addressPolicy{appID: v.appID}).repliesToSelf(msg) || v.reply == contracts.ReplyInChannels && v.channels.matches(msg.ChannelID, parent)
}

func (r *router) blankMayMatter(ctrl contracts.SessionControl, msg messageCreate, parent string) bool {
	chatType := r.chatType(msg.ChannelID, msg.direct())
	if r.mode(msg.ChannelID, parent) == ambientMode || r.cfg.address.addressed(msg, chatType, parent) {
		return true
	}
	for _, route := range r.voiceRoutes(ctrl) {
		if route.mayMatterBlank(msg, parent) {
			return true
		}
	}
	return false
}

func directoryVoices(ctrl contracts.SessionControl) []contracts.AgentVoice {
	directory, ok := ctrl.(contracts.AgentDirectory)
	if !ok {
		return nil
	}
	return directory.AgentVoices(gatewayKind)
}

type routeCache struct {
	mu      sync.Mutex
	primed  bool
	version uint64
	voices  []contracts.AgentVoice
	routes  []voiceRoute
}

func (c *routeCache) of(voices []contracts.AgentVoice, version uint64, build func([]contracts.AgentVoice) []voiceRoute) []voiceRoute {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.primed && c.version == version && sameVoices(c.voices, voices) {
		return c.routes
	}
	c.voices, c.routes, c.version, c.primed = cloneVoices(voices), build(voices), version, true
	return c.routes
}

func sameVoices(left, right []contracts.AgentVoice) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Agent != right[index].Agent || left[index].Reply != right[index].Reply || !slices.Equal(left[index].Channels, right[index].Channels) {
			return false
		}
	}
	return true
}

func cloneVoices(voices []contracts.AgentVoice) []contracts.AgentVoice {
	out := make([]contracts.AgentVoice, len(voices))
	for index, voice := range voices {
		out[index] = voice
		out[index].Channels = slices.Clone(voice.Channels)
	}
	return out
}

func buildRoutes(voices []contracts.AgentVoice, bots *voiceBots) []voiceRoute {
	var routes []voiceRoute
	for _, voice := range voices {
		if !validAgent(voice.Agent) {
			continue
		}
		bot, own := bots.ownOf(voice.Agent)
		routes = append(routes, voiceRoute{
			agent:    voice.Agent,
			reply:    voice.Reply,
			channels: channelSet(voice.Channels),
			mention:  newAgentMention(voice.Agent),
			own:      own,
			pending:  bots.resolvingAgent(voice.Agent),
			appID:    bot.appID,
		})
	}
	return routes
}

func (r *router) voiceRoutes(ctrl contracts.SessionControl) []voiceRoute {
	return r.routes.of(directoryVoices(ctrl), r.cfg.voices.version(), func(voices []contracts.AgentVoice) []voiceRoute {
		return buildRoutes(voices, r.cfg.voices)
	})
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

func (r *router) pendingAgentHolds(ctrl contracts.SessionControl, msg messageCreate, parent string) bool {
	if r.cfg.address.mentioned(msg) {
		return false
	}
	for _, route := range r.voiceRoutes(ctrl) {
		if !route.pending || route.reply == contracts.ReplyNever {
			continue
		}
		if route.mention.matches(msg.Content) || route.reply == contracts.ReplyInChannels && route.channels.matches(msg.ChannelID, parent) {
			return true
		}
	}
	return false
}

func (r *router) addressesAgentBot(msg messageCreate) bool {
	if r.cfg.address.mentioned(msg) {
		return false
	}
	if r.cfg.voices.awaiting() {
		return mentionsAnyBot(msg)
	}
	for _, appID := range r.cfg.voices.agentAppIDs() {
		if mentionsID(msg, appID) {
			return true
		}
	}
	return false
}

func mentionsAnyBot(msg messageCreate) bool {
	for _, u := range msg.Mentions {
		if u.Bot {
			return true
		}
	}
	return false
}

func (r *router) houseVoice(ctrl contracts.SessionControl, msg messageCreate, parent string) (voiceRoute, bool) {
	routes := r.voiceRoutes(ctrl)
	for _, route := range routes {
		if route.answersThroughHouse() && route.mention.matches(msg.Content) {
			return route, true
		}
	}
	if !r.houseContent.Load() {
		return voiceRoute{}, false
	}
	for _, route := range routes {
		if route.answersThroughHouse() && route.reply == contracts.ReplyInChannels && route.channels.matches(msg.ChannelID, parent) {
			return route, true
		}
	}
	return voiceRoute{}, false
}

func (r *router) onAgentMessage(ctx context.Context, agent string, msg messageCreate, content bool) {
	ctrl := r.ctrl()
	if ctrl == nil {
		return
	}
	route, ok := r.routeOf(ctrl, agent)
	if !ok || route.reply == contracts.ReplyNever {
		return
	}
	parent := r.binds.Parent(msg.ChannelID)
	if r.mode(msg.ChannelID, parent) == offMode || !r.acl.permits(msg, parent, true) {
		return
	}
	msg, ok = r.hydrated(ctx, ctrl, msg, parent, voiced(msg.ChannelID, agent))
	if !ok || !r.voiceAddressed(ctrl, route, msg, parent, content) {
		return
	}
	r.answerAs(ctx, ctrl, route, msg)
}

func (r *router) voiceAddressed(ctrl contracts.SessionControl, route voiceRoute, msg messageCreate, parent string, content bool) bool {
	if msg.direct() || mentionsID(msg, route.appID) {
		return true
	}
	if (addressPolicy{appID: route.appID}).repliesToSelf(msg) {
		return true
	}
	return content && route.reply == contracts.ReplyInChannels && route.channels.matches(msg.ChannelID, parent) && !r.addressesAnotherBot(ctrl, route, msg)
}

func (r *router) addressesAnotherBot(ctrl contracts.SessionControl, route voiceRoute, msg messageCreate) bool {
	for _, appID := range r.cfg.voices.agentAppIDs() {
		if appID != route.appID && mentionsID(msg, appID) {
			return true
		}
	}
	return r.cfg.address.mentioned(msg) || namedTokenless(r.voiceRoutes(ctrl), msg.Content)
}

func namedTokenless(routes []voiceRoute, content string) bool {
	for _, route := range routes {
		if !route.own && route.mention.matches(content) {
			return true
		}
	}
	return false
}

func (r *router) mentionedBy(route voiceRoute, msg messageCreate) bool {
	if route.own {
		return mentionsID(msg, route.appID)
	}
	return route.mention.matches(msg.Content)
}

func (r *router) answerAs(ctx context.Context, ctrl contracts.SessionControl, route voiceRoute, msg messageCreate) {
	bare := msg.ChannelID
	parent := r.binds.Parent(bare)
	if !r.acl.permits(msg, parent, r.mentionedBy(route, msg)) {
		return
	}
	conv := voiced(bare, route.agent)
	msg.ChannelID = conv
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

func (r *router) voiceCaptured(ctx context.Context, ctrl contracts.SessionControl, msg messageCreate, parent string) bool {
	route, ok := r.houseVoice(ctrl, msg, parent)
	if !ok {
		return false
	}
	r.answerAs(ctx, ctrl, route, msg)
	return !r.cfg.address.mentioned(msg)
}

func (r *router) setHouseContent(on bool) {
	r.houseContent.Store(on)
}
