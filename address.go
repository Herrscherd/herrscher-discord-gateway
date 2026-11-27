package discord

import "regexp"

var rawMention = regexp.MustCompile(`<@!?(\d+)>`)

type addressPolicy struct {
	appID                string
	requireMention       bool
	threadRequireMention bool
	free                 idSet
}

func defaultAddress(appID string) addressPolicy {
	return addressPolicy{appID: appID, requireMention: true}
}

func (p addressPolicy) mentioned(msg messageCreate) bool {
	return mentionsID(msg, p.appID)
}

func (p addressPolicy) repliesToSelf(m messageCreate) bool {
	return p.appID != "" && m.Referenced != nil && m.Referenced.Author.ID == p.appID
}

func (p addressPolicy) addressed(msg messageCreate, chatType, parent string) bool {
	if p.mentioned(msg) || p.repliesToSelf(msg) {
		return true
	}
	if chatType == chatDM {
		return true
	}
	if chatType == chatThread && !p.threadRequireMention {
		return true
	}
	if p.free.matches(msg.ChannelID, parent) {
		return true
	}
	return !p.requireMention
}
