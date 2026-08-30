package discord

import "strings"

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
	return "ch-" + strings.ReplaceAll(slot, ":", "-")
}
