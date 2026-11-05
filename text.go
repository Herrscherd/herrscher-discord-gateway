package discord

import (
	"embed"
	"errors"
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
	unexpectedKey        = "notice.unexpected"
	noDetailKey          = "notice.no_detail"
	unknownChannelKey    = "channel.unknown"
)

var errUndeclaredKey = errors.New("undeclared message key")

type messages struct {
	catalog  *i18n.Catalog
	renderer *contracts.Renderer
	heads    heads
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
	tables, err := embeddedHeads()
	if err != nil {
		return messages{}, err
	}
	return messages{catalog: catalog, renderer: contracts.NewRenderer(), heads: tables, lang: lang, log: log}, nil
}

func (m messages) in(lang i18n.Language) messages {
	m.lang = lang
	return m
}

func (m messages) head(key headKey) string {
	return m.headIn(m.lang, key)
}

func (m messages) headIn(lang i18n.Language, key headKey) string {
	if head, ok := m.heads[lang][string(key)]; ok {
		return head
	}
	return m.heads[i18n.English][string(key)]
}

func (m messages) say(key string, params map[string]any) string {
	return m.sayIn(m.lang, key, params)
}

func (m messages) sayIn(lang i18n.Language, key string, params map[string]any) string {
	if !m.catalog.Has(key) {
		return m.failed(lang, key, errUndeclaredKey)
	}
	text, err := m.catalog.Message(lang, key, params)
	if err != nil {
		return m.failed(lang, key, err)
	}
	return text
}

func (m messages) failed(lang i18n.Language, key string, err error) string {
	m.log.Warn("message not rendered", "key", key, "err", err)
	return m.unexpectedDetail(lang, fmt.Sprintf("%s: %v", key, err))
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
	if err != nil {
		return m.renderFailed(lang, notice, err)
	}
	return text
}

func (m messages) renderFailed(lang i18n.Language, notice contracts.Notice, err error) string {
	m.log.Warn("notice not rendered", "code", notice.Code.Key(), "err", err)
	return m.unexpectedDetail(lang, err.Error())
}

func (m messages) unexpected(lang i18n.Language, notice contracts.Notice) string {
	return m.unexpectedDetail(lang, detailOf(notice))
}

func (m messages) unexpectedDetail(lang i18n.Language, detail string) string {
	intro := m.catalogText(lang, unexpectedKey)
	if detail == "" {
		return intro + "\n" + m.catalogText(lang, noDetailKey)
	}
	return intro + "\n" + codeBlock(detail)
}

func (m messages) catalogText(lang i18n.Language, key string) string {
	text, err := m.catalog.Message(lang, key, nil)
	if err != nil {
		m.log.Error("built-in message missing", "err", err)
		return ""
	}
	return text
}

func (m messages) channelRef(lang i18n.Language, channelID string) string {
	if channelID == "" {
		return m.catalogText(lang, unknownChannelKey)
	}
	return fmt.Sprintf(channelMentionFormat, channelID)
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
	return "```\n" + clip(neutraliseCode(detail), detailMaxRunes) + "\n```"
}

func neutraliseCode(text string) string {
	return strings.NewReplacer("`", "`"+zeroWidthSpace, "@", "@"+zeroWidthSpace).Replace(text)
}

func neutralise(text string) string {
	return strings.NewReplacer(
		"`", "`"+zeroWidthSpace,
		"@", "@"+zeroWidthSpace,
		`\`, `\\`,
		"*", `\*`,
		"_", `\_`,
		"~", `\~`,
		"|", `\|`,
		"[", `\[`,
		"]", `\]`,
		"(", `\(`,
		")", `\)`,
		"#", `\#`,
		">", `\>`,
	).Replace(text)
}

func plainNotice(notice contracts.Notice) contracts.Notice {
	args := make([]contracts.Arg, 0, len(notice.Args))
	for _, arg := range notice.Args {
		switch arg.Kind() {
		case contracts.ArgText:
			args = append(args, contracts.Text(arg.Name(), neutralise(fmt.Sprint(arg.Value()))))
		case contracts.ArgRef:
			args = append(args, contracts.Ref(arg.Name(), neutralise(fmt.Sprint(arg.Value()))))
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

func withEmoji(emoji, text string) string {
	if emoji == "" {
		return text
	}
	return emoji + " " + text
}
