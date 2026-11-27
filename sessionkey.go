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
	name := "ch-" + sessionBody(slot)
	if len(name) <= sessionNameMax {
		return name
	}
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(slot))
	suffix := fmt.Sprintf("-%0*x", sessionHashDigits, sum.Sum64())
	return name[:sessionNameMax-len(suffix)] + suffix
}

func sessionBody(slot string) string {
	conv, user, hasUser := strings.Cut(slot, ":")
	channel, agent, voicedConv := strings.Cut(conv, voiceSeparator)
	body := channel
	if hasUser {
		body += "-" + user
	}
	if voicedConv {
		body += sessionVoiceMarker + agent
	}
	return body
}
