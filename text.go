package discord

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"

	contracts "github.com/Herrscherd/herrscher-contracts"
	"github.com/Herrscherd/herrscher-contracts/i18n"
)

//go:embed locales
var localeFS embed.FS

const (
	countArg             = "Count"
	detailArg            = "Detail"
	mentionArg           = "Mention"
	nameArg              = "Name"
	detailMaxRunes       = 1500
	zeroWidthSpace       = "\u200b"
	channelMentionFormat = "<#%s>"
)

type messages struct {
	catalog  *i18n.Catalog
	renderer *contracts.Renderer
	lang     i18n.Language
	log      *slog.Logger
}

func newMessages(lang i18n.Language, log *slog.Logger) (messages, error) {
	sources, err := fs.Sub(localeFS, "locales")
	if err != nil {
		return messages{}, fmt.Errorf("discord gateway: embedded locales: %w", err)
	}
	catalog, err := i18n.NewCatalog(log, sources)
	if err != nil {
		return messages{}, fmt.Errorf("discord gateway: message catalog: %w", err)
	}
	return messages{catalog: catalog, renderer: contracts.NewRenderer(), lang: lang, log: log}, nil
}

func (m messages) say(key string, params map[string]any) string {
	return m.sayIn(m.lang, key, params)
}

func (m messages) sayIn(lang i18n.Language, key string, params map[string]any) string {
	text, err := m.catalog.Message(lang, key, params)
	if err != nil {
		m.log.Warn("message not rendered", "key", key, "err", err)
		return key
	}
	return text
}

func (m messages) notice(lang i18n.Language, notice contracts.Notice) (string, error) {
	if notice.Code == contracts.CodeUnexpected {
		return m.unexpected(lang, notice), nil
	}
	text, err := m.renderer.Render(lang, plainNotice(notice))
	if err != nil {
		return "", fmt.Errorf("render notice %s: %w", notice.Code.Key(), err)
	}
	return text, nil
}

func (m messages) noticeText(lang i18n.Language, notice contracts.Notice) string {
	text, err := m.notice(lang, notice)
	if err == nil {
		return text
	}
	m.log.Warn("notice not rendered", "code", notice.Code.Key(), "err", err)
	return m.unexpected(lang, contracts.NoticeOf(err))
}

func (m messages) unexpected(lang i18n.Language, notice contracts.Notice) string {
	return m.sayIn(lang, "notice.unexpected", nil) + "\n" + codeBlock(detailOf(notice))
}

func detailOf(notice contracts.Notice) string {
	for _, arg := range notice.Args {
		if arg.Name() == detailArg {
			return fmt.Sprint(arg.Value())
		}
	}
	return ""
}

func codeBlock(detail string) string {
	return "```\n" + clip(neutralise(detail), detailMaxRunes) + "\n```"
}

func neutralise(text string) string {
	return strings.NewReplacer("`", "`"+zeroWidthSpace, "@", "@"+zeroWidthSpace).Replace(text)
}

func plainNotice(notice contracts.Notice) contracts.Notice {
	args := make([]contracts.Arg, 0, len(notice.Args))
	for _, arg := range notice.Args {
		switch arg.Kind() {
		case contracts.ArgRef:
			args = append(args, contracts.Text(arg.Name(), fmt.Sprintf(channelMentionFormat, arg.Value())))
		case contracts.ArgText:
			args = append(args, contracts.Text(arg.Name(), neutralise(fmt.Sprint(arg.Value()))))
		case contracts.ArgCount:
			args = append(args, arg)
		}
	}
	return contracts.Notice{Code: notice.Code, Args: args}
}

func (m messages) resolve(lang i18n.Language) i18n.Language {
	if lang == 0 {
		return m.lang
	}
	return lang
}

func noticeEmoji(code contracts.Code) string {
	switch code {
	case contracts.CodeSessionCreated:
		return "✅"
	case contracts.CodeSessionClosed:
		return "⏹️"
	case contracts.CodeSessionNotFound, contracts.CodeSessionExists, contracts.CodeUnknownAgent, contracts.CodeUnexpected:
		return failEmoji
	}
	return failEmoji
}
