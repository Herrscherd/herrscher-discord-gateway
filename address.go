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
	if p.appID == "" {
		return false
	}
	for _, u := range msg.Mentions {
		if u.ID == p.appID {
			return true
		}
	}
	for _, hit := range rawMention.FindAllStringSubmatch(msg.Content, -1) {
		if hit[1] == p.appID {
			return true
		}
	}
	return false
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
