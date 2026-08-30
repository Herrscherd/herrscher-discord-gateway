package discord

import (
	"fmt"
	"os"
	"sync"
)

const (
	botsNone     = "none"
	botsMentions = "mentions"
	botsAll      = "all"
)

func knownBotPolicy(v string) bool {
	switch v {
	case botsNone, botsMentions, botsAll:
		return true
	}
	return false
}

type accessPolicy struct {
	owner    string
	selfID   string
	users    idSet
	roles    idSet
	channels idSet
	ignored  idSet
	allowAll bool
	bots     string

	warn sync.Once
}

func (p *accessPolicy) configured() bool {
	return p.owner != "" || !p.users.empty() || !p.roles.empty() || !p.channels.empty() || p.allowAll
}

func (p *accessPolicy) permits(m messageCreate, parent string, mentioned bool) bool {
	if p.selfID != "" && m.Author.ID == p.selfID {
		return false
	}
	if m.Author.Bot {
		switch p.bots {
		case botsAll:
		case botsMentions:
			if !mentioned {
				return false
			}
		default:
			return false
		}
	}
	if p.ignored.matches(m.ChannelID, parent) {
		return false
	}
	if !p.channels.empty() && !p.channels.matches(m.ChannelID, parent) {
		return false
	}
	return p.authorizes(m)
}

func (p *accessPolicy) authorizes(m messageCreate) bool {
	if p.owner != "" && m.Author.ID == p.owner {
		return true
	}
	if p.users.matches(m.Author.ID) {
		return true
	}
	if !p.roles.empty() && !m.direct() && p.roles.matches(m.roleIDs()...) {
		return true
	}
	if p.allowAll {
		return true
	}
	if p.users.empty() && p.roles.empty() && p.owner == "" && !p.channels.empty() {
		return true
	}
	p.warnUnconfigured()
	return false
}

func (p *accessPolicy) warnUnconfigured() {
	if p.configured() {
		return
	}
	p.warn.Do(func() {
		fmt.Fprintln(os.Stderr, "discord gateway: nobody is authorized, so every message is ignored. "+
			"Set DISCORD_USER_ID, DISCORD_ALLOWED_USERS, DISCORD_ALLOWED_ROLES or DISCORD_ALLOWED_CHANNELS.")
	})
}
