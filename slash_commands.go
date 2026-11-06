package discord

import (
	"github.com/Herrscherd/dctl"
	"github.com/Herrscherd/herrscher-contracts/i18n"
)

type labelSet struct {
	texts map[i18n.Language]map[string]string
}

func newLabels(msgs messages) labelSet {
	return labelSet{texts: map[i18n.Language]map[string]string{
		i18n.English: slashTextsIn(msgs, i18n.English),
		i18n.French:  slashTextsIn(msgs, i18n.French),
	}}
}

func slashTextsIn(msgs messages, lang i18n.Language) map[string]string {
	return map[string]string{
		"discord.slash.set":                       msgs.sayIn(lang, "discord.slash.set", nil),
		"discord.slash.set.home":                  msgs.sayIn(lang, "discord.slash.set.home", nil),
		"discord.slash.set.home.channel":          msgs.sayIn(lang, "discord.slash.set.home.channel", nil),
		"discord.slash.set.source":                msgs.sayIn(lang, "discord.slash.set.source", nil),
		"discord.slash.set.source.path":           msgs.sayIn(lang, "discord.slash.set.source.path", nil),
		"discord.slash.session":                   msgs.sayIn(lang, "discord.slash.session", nil),
		"discord.slash.session.create":            msgs.sayIn(lang, "discord.slash.session.create", nil),
		"discord.slash.session.create.name":       msgs.sayIn(lang, "discord.slash.session.create.name", nil),
		"discord.slash.session.create.cmd":        msgs.sayIn(lang, "discord.slash.session.create.cmd", nil),
		"discord.slash.session.create.shared":     msgs.sayIn(lang, "discord.slash.session.create.shared", nil),
		"discord.slash.session.create.backend":    msgs.sayIn(lang, "discord.slash.session.create.backend", nil),
		"discord.slash.session.create.project":    msgs.sayIn(lang, "discord.slash.session.create.project", nil),
		"discord.slash.session.create.clone":      msgs.sayIn(lang, "discord.slash.session.create.clone", nil),
		"discord.slash.session.close":             msgs.sayIn(lang, "discord.slash.session.close", nil),
		"discord.slash.session.close.name":        msgs.sayIn(lang, "discord.slash.session.close.name", nil),
		"discord.slash.session.close.force":       msgs.sayIn(lang, "discord.slash.session.close.force", nil),
		"discord.slash.session.list":              msgs.sayIn(lang, "discord.slash.session.list", nil),
		"discord.slash.session.who":               msgs.sayIn(lang, "discord.slash.session.who", nil),
		"discord.slash.session.who.name":          msgs.sayIn(lang, "discord.slash.session.who.name", nil),
		"discord.slash.session.allow":             msgs.sayIn(lang, "discord.slash.session.allow", nil),
		"discord.slash.session.allow.add":         msgs.sayIn(lang, "discord.slash.session.allow.add", nil),
		"discord.slash.session.allow.add.name":    msgs.sayIn(lang, "discord.slash.session.allow.add.name", nil),
		"discord.slash.session.allow.add.user":    msgs.sayIn(lang, "discord.slash.session.allow.add.user", nil),
		"discord.slash.session.allow.remove":      msgs.sayIn(lang, "discord.slash.session.allow.remove", nil),
		"discord.slash.session.allow.remove.name": msgs.sayIn(lang, "discord.slash.session.allow.remove.name", nil),
		"discord.slash.session.allow.remove.user": msgs.sayIn(lang, "discord.slash.session.allow.remove.user", nil),
		"discord.slash.session.allow.list":        msgs.sayIn(lang, "discord.slash.session.allow.list", nil),
		"discord.slash.session.allow.list.name":   msgs.sayIn(lang, "discord.slash.session.allow.list.name", nil),
		"discord.slash.service":                   msgs.sayIn(lang, "discord.slash.service", nil),
		"discord.slash.service.restart":           msgs.sayIn(lang, "discord.slash.service.restart", nil),
		"discord.slash.service.update":            msgs.sayIn(lang, "discord.slash.service.update", nil),
		"discord.slash.service.update.no_pull":    msgs.sayIn(lang, "discord.slash.service.update.no_pull", nil),
		"discord.slash.stop":                      msgs.sayIn(lang, "discord.slash.stop", nil),
		"discord.slash.verbosity":                 msgs.sayIn(lang, "discord.slash.verbosity", nil),
		"discord.slash.verbosity.level":           msgs.sayIn(lang, "discord.slash.verbosity.level", nil),
		"discord.slash.verbosity.level.silent":    msgs.sayIn(lang, "discord.slash.verbosity.level.silent", nil),
		"discord.slash.verbosity.level.quiet":     msgs.sayIn(lang, "discord.slash.verbosity.level.quiet", nil),
		"discord.slash.verbosity.level.actions":   msgs.sayIn(lang, "discord.slash.verbosity.level.actions", nil),
		"discord.slash.verbosity.level.full":      msgs.sayIn(lang, "discord.slash.verbosity.level.full", nil),
		"discord.slash.mode":                      msgs.sayIn(lang, "discord.slash.mode", nil),
		"discord.slash.mode.mode":                 msgs.sayIn(lang, "discord.slash.mode.mode", nil),
		"discord.slash.mode.mode.normal":          msgs.sayIn(lang, "discord.slash.mode.mode.normal", nil),
		"discord.slash.mode.mode.support":         msgs.sayIn(lang, "discord.slash.mode.mode.support", nil),
		"discord.slash.mode.mode.ambient":         msgs.sayIn(lang, "discord.slash.mode.mode.ambient", nil),
		"discord.slash.mode.mode.off":             msgs.sayIn(lang, "discord.slash.mode.mode.off", nil),
		"discord.slash.allow":                     msgs.sayIn(lang, "discord.slash.allow", nil),
		"discord.slash.allow.add":                 msgs.sayIn(lang, "discord.slash.allow.add", nil),
		"discord.slash.allow.add.user":            msgs.sayIn(lang, "discord.slash.allow.add.user", nil),
		"discord.slash.allow.remove":              msgs.sayIn(lang, "discord.slash.allow.remove", nil),
		"discord.slash.allow.remove.user":         msgs.sayIn(lang, "discord.slash.allow.remove.user", nil),
		"discord.slash.allow.list":                msgs.sayIn(lang, "discord.slash.allow.list", nil),
	}
}

func discordLocale(lang i18n.Language) (dctl.Locale, bool) {
	switch lang {
	case i18n.English:
		return "", false
	case i18n.French:
		return dctl.LocaleFR, true
	}
	return "", false
}

func (lab labelSet) english(key string) string {
	return lab.texts[i18n.English][key]
}

func (lab labelSet) translations(key string, apply func(dctl.Locale, string)) {
	for _, lang := range i18n.Languages() {
		locale, ok := discordLocale(lang)
		if !ok {
			continue
		}
		apply(locale, lab.texts[lang][key])
	}
}

func (lab labelSet) command(name, key string) *dctl.Command {
	cmd := dctl.NewCommand(name, lab.english(key))
	lab.translations(key, func(locale dctl.Locale, text string) {
		cmd.Loc(locale, name, text)
	})
	return cmd
}

func (lab labelSet) option(opt dctl.Option, name, key string) dctl.Option {
	lab.translations(key, func(locale dctl.Locale, text string) {
		opt = opt.Loc(locale, name, text)
	})
	return opt
}

func (lab labelSet) choice(value any, key string) dctl.Choice {
	choice := dctl.NewChoice(lab.english(key), value)
	lab.translations(key, func(locale dctl.Locale, text string) {
		choice = choice.Loc(locale, text)
	})
	return choice
}

func (lab labelSet) sub(name, key string, opts ...dctl.Option) dctl.Option {
	return lab.option(dctl.Sub(name, lab.english(key), opts...), name, key)
}

func (lab labelSet) group(name, key string, subs ...dctl.Option) dctl.Option {
	return lab.option(dctl.Group(name, lab.english(key), subs...), name, key)
}

func (lab labelSet) str(name, key string, required bool) dctl.Option {
	return lab.option(dctl.String(name, lab.english(key), required), name, key)
}

func (lab labelSet) boolean(name, key string, required bool) dctl.Option {
	return lab.option(dctl.Bool(name, lab.english(key), required), name, key)
}

func (lab labelSet) user(key string) dctl.Option {
	return lab.option(dctl.User("user", lab.english(key), true), "user", key)
}

func (lab labelSet) channel(name, key string, required bool) dctl.Option {
	return lab.option(dctl.ChannelOpt(name, lab.english(key), required), name, key)
}

func commandSet(lab labelSet) *dctl.Command {
	return lab.command("set", "discord.slash.set").
		Perms(dctl.PermManageGuild).
		With(
			lab.sub("home", "discord.slash.set.home",
				lab.channel("channel", "discord.slash.set.home.channel", true)),
			lab.sub("source", "discord.slash.set.source",
				lab.str("path", "discord.slash.set.source.path", true)),
		)
}

func commandSession(lab labelSet) *dctl.Command {
	return lab.command("session", "discord.slash.session").
		Perms(dctl.PermManageGuild).
		With(
			lab.sub("create", "discord.slash.session.create",
				lab.str("name", "discord.slash.session.create.name", true),
				lab.str("cmd", "discord.slash.session.create.cmd", false),
				lab.boolean("shared", "discord.slash.session.create.shared", false),
				lab.str("backend", "discord.slash.session.create.backend", false).
					Choices(dctl.NewChoice("stream", "stream"), dctl.NewChoice("oneshot", "oneshot")),
				lab.str("project", "discord.slash.session.create.project", false),
				lab.str("clone", "discord.slash.session.create.clone", false),
			),
			lab.sub("close", "discord.slash.session.close",
				lab.str("name", "discord.slash.session.close.name", true).Autocomplete(),
				lab.boolean("force", "discord.slash.session.close.force", false),
			),
			lab.sub("list", "discord.slash.session.list"),
			lab.sub("who", "discord.slash.session.who",
				lab.str("name", "discord.slash.session.who.name", true).Autocomplete()),
			lab.group("allow", "discord.slash.session.allow",
				lab.sub("add", "discord.slash.session.allow.add",
					lab.str("name", "discord.slash.session.allow.add.name", true).Autocomplete(),
					lab.user("discord.slash.session.allow.add.user")),
				lab.sub("remove", "discord.slash.session.allow.remove",
					lab.str("name", "discord.slash.session.allow.remove.name", true).Autocomplete(),
					lab.user("discord.slash.session.allow.remove.user")),
				lab.sub("list", "discord.slash.session.allow.list",
					lab.str("name", "discord.slash.session.allow.list.name", true).Autocomplete()),
			),
		)
}

func commandService(lab labelSet) *dctl.Command {
	return lab.command("service", "discord.slash.service").
		Perms(dctl.PermManageGuild).
		With(
			lab.sub("restart", "discord.slash.service.restart"),
			lab.sub("update", "discord.slash.service.update",
				lab.boolean("no_pull", "discord.slash.service.update.no_pull", false)),
		)
}

func commandStop(lab labelSet) *dctl.Command {
	return lab.command("stop", "discord.slash.stop").
		Perms(dctl.PermManageGuild)
}

func commandVerbosity(lab labelSet) *dctl.Command {
	return lab.command("verbosity", "discord.slash.verbosity").
		Perms(dctl.PermManageGuild).
		With(
			lab.str("level", "discord.slash.verbosity.level", true).
				Choices(
					lab.choice(levelSilent, "discord.slash.verbosity.level.silent"),
					lab.choice(levelQuiet, "discord.slash.verbosity.level.quiet"),
					lab.choice(levelActions, "discord.slash.verbosity.level.actions"),
					lab.choice(levelFull, "discord.slash.verbosity.level.full"),
				),
		)
}

func commandMode(lab labelSet) *dctl.Command {
	return lab.command("mode", "discord.slash.mode").
		Perms(dctl.PermManageGuild).
		With(
			lab.str("mode", "discord.slash.mode.mode", true).
				Choices(
					lab.choice(normalMode, "discord.slash.mode.mode.normal"),
					lab.choice(supportMode, "discord.slash.mode.mode.support"),
					lab.choice(ambientMode, "discord.slash.mode.mode.ambient"),
					lab.choice(offMode, "discord.slash.mode.mode.off"),
				),
		)
}

func commandAllow(lab labelSet) *dctl.Command {
	return lab.command("allow", "discord.slash.allow").
		Perms(dctl.PermManageGuild).
		With(
			lab.sub("add", "discord.slash.allow.add", lab.user("discord.slash.allow.add.user")),
			lab.sub("remove", "discord.slash.allow.remove", lab.user("discord.slash.allow.remove.user")),
			lab.sub("list", "discord.slash.allow.list"),
		)
}
