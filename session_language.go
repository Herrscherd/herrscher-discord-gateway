package discord

import (
	contracts "github.com/Herrscherd/herrscher-contracts"
	"github.com/Herrscherd/herrscher-contracts/i18n"
)

type sessionLanguages struct {
	ctrl  func() contracts.SessionControl
	binds *bindStore
}

func (l sessionLanguages) of(conv string) i18n.Language {
	if l.ctrl == nil || l.binds == nil {
		return 0
	}
	ctrl := l.ctrl()
	if ctrl == nil {
		return 0
	}
	name := l.binds.Session(conv)
	items := ctrl.Sessions()
	for index := range items {
		if items[index].ChannelID == conv || (name != "" && items[index].Name == name) {
			return items[index].Language
		}
	}
	return 0
}

func (l sessionLanguages) pick(conv string, fallback i18n.Language) i18n.Language {
	if lang := l.of(conv); lang != 0 {
		return lang
	}
	return fallback
}

func (l sessionLanguages) messagesFor(conv string, msgs messages) messages {
	if lang := l.of(conv); lang != 0 {
		return msgs.in(lang)
	}
	return msgs
}
