package discord

import (
	"fmt"
	"hash/fnv"
	"strings"
)

const (
	chatDM      = "dm"
	chatThread  = "thread"
	chatChannel = "channel"
)

type sessionScope struct {
	groupPerUser  bool
	threadPerUser bool
}

func defaultScope() sessionScope { return sessionScope{groupPerUser: true} }

func (s sessionScope) isolatesUser(chatType string) bool {
	switch chatType {
	case chatDM:
		return false
	case chatThread:
		return s.threadPerUser
	default:
		return s.groupPerUser
	}
}

func (s sessionScope) multiUser(chatType string) bool {
	if chatType == chatDM {
		return false
	}
	return !s.isolatesUser(chatType)
}

func (s sessionScope) slot(chatType, conv, user string) string {
	if conv == "" {
		return ""
	}
	if s.isolatesUser(chatType) && user != "" {
		return conv + ":" + user
	}
	return conv
}

func convOfSlot(slot string) string {
	conv, _, _ := strings.Cut(slot, ":")
	return conv
}

func sessionNameFor(slot string) string {
	if !snowflakeSlot(slot) {
		return hashedSessionName(slot)
	}
	name := "ch-" + sessionBody(slot)
	if len(name) <= sessionNameMax {
		return name
	}
	return hashedSessionName(slot)
}

func hashedSessionName(slot string) string {
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(slot))
	return fmt.Sprintf("%s%0*x", sessionHashPrefix, sessionHashDigits, sum.Sum64())
}

func snowflakeSlot(slot string) bool {
	conv, user, hasUser := strings.Cut(slot, ":")
	channel, _, _ := strings.Cut(conv, voiceSeparator)
	return isSnowflake(channel) && (!hasUser || isSnowflake(user))
}

func isSnowflake(id string) bool {
	return id != "" && strings.IndexFunc(id, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

func sessionBody(slot string) string {
	conv, user, hasUser := strings.Cut(slot, ":")
	channel, agent, voiced := strings.Cut(conv, voiceSeparator)
	body := channel
	if hasUser {
		body += "-" + user
	}
	if voiced {
		body += sessionVoiceMarker + agent
	}
	return body
}
