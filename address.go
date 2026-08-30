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

func (p addressPolicy) mentioned(m messageCreate) bool {
	if p.appID == "" {
		return false
	}
	for _, u := range m.Mentions {
		if u.ID == p.appID {
			return true
		}
	}
	for _, hit := range rawMention.FindAllStringSubmatch(m.Content, -1) {
		if hit[1] == p.appID {
			return true
		}
	}
	return false
}

func (p addressPolicy) repliesToSelf(m messageCreate) bool {
	return p.appID != "" && m.Referenced != nil && m.Referenced.Author.ID == p.appID
}

func (p addressPolicy) addressed(m messageCreate, chatType, parent string) bool {
	if p.mentioned(m) || p.repliesToSelf(m) {
		return true
	}
	if chatType == chatDM {
		return true
	}
	if chatType == chatThread && !p.threadRequireMention {
		return true
	}
	if p.free.matches(m.ChannelID, parent) {
		return true
	}
	return !p.requireMention
}
