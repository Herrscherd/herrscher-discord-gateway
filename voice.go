package discord

import (
	"errors"
	"regexp"
	"strings"
)

const (
	voiceSeparator     = "@"
	sessionNameMax     = 64
	sessionHashDigits  = 16
	sessionVoiceMarker = "-a-"
	agentNamePattern   = `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`
)

var (
	agentName    = regexp.MustCompile(agentNamePattern)
	errBadVoiced = errors.New("bad voiced conversation address")
	errTooLong   = errors.New("message over the Discord limit")
)

type voiceAddress struct {
	channel string
	agent   string
}

func voiced(channel, agent string) string {
	if agent == "" {
		return channel
	}
	return channel + voiceSeparator + agent
}

func parseAddress(conv string) (voiceAddress, error) {
	channel, agent, voicedAddress := strings.Cut(conv, voiceSeparator)
	if channel == "" {
		return voiceAddress{}, errBadVoiced
	}
	if !voicedAddress {
		return voiceAddress{channel: channel}, nil
	}
	if !validAgent(agent) {
		return voiceAddress{}, errBadVoiced
	}
	return voiceAddress{channel: channel, agent: agent}, nil
}

func addressOf(conv string) voiceAddress {
	addr, err := parseAddress(conv)
	if err != nil {
		return voiceAddress{channel: conv}
	}
	return addr
}

func (a voiceAddress) String() string { return voiced(a.channel, a.agent) }

func validAgent(name string) bool { return agentName.MatchString(name) }

func agentHeader(agent string) string {
	if agent == "" {
		return ""
	}
	return "**" + neutralise(agent) + "**\n"
}

type agentMention struct{ pattern *regexp.Regexp }

func newAgentMention(agent string) agentMention {
	return agentMention{pattern: regexp.MustCompile(`(?i)(?:^|\s)@` + regexp.QuoteMeta(agent) + `(?:$|[^\p{L}\p{N}_-])`)}
}

func (m agentMention) matches(text string) bool {
	return m.pattern.MatchString(text)
}

func mentionsID(msg messageCreate, appID string) bool {
	if appID == "" {
		return false
	}
	for _, u := range msg.Mentions {
		if u.ID == appID {
			return true
		}
	}
	for _, hit := range rawMention.FindAllStringSubmatch(msg.Content, -1) {
		if hit[1] == appID {
			return true
		}
	}
	return false
}
