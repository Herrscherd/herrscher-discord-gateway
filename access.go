package discord

import (
	"log/slog"
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
	log      *slog.Logger
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

func (p *accessPolicy) permits(msg messageCreate, parent string, mentioned bool) bool {
	if p.selfID != "" && msg.Author.ID == p.selfID {
		return false
	}
	if msg.Author.Bot {
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
	if p.ignored.matches(msg.ChannelID, parent) {
		return false
	}
	if !p.channels.empty() && !p.channels.matches(msg.ChannelID, parent) {
		return false
	}
	return p.authorizes(msg)
}

func (p *accessPolicy) authorizes(msg messageCreate) bool {
	if p.owner != "" && msg.Author.ID == p.owner {
		return true
	}
	if p.users.matches(msg.Author.ID) {
		return true
	}
	if !p.roles.empty() && !msg.direct() && p.roles.matches(msg.roleIDs()...) {
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
		p.log.Warn("nobody is authorized, so every message is ignored; set DISCORD_USER_ID, DISCORD_ALLOWED_USERS, DISCORD_ALLOWED_ROLES or DISCORD_ALLOWED_CHANNELS")
	})
}
