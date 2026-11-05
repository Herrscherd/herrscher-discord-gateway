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
		"slash.set":                       msgs.sayIn(lang, "slash.set", nil),
		"slash.set.home":                  msgs.sayIn(lang, "slash.set.home", nil),
		"slash.set.home.channel":          msgs.sayIn(lang, "slash.set.home.channel", nil),
		"slash.set.source":                msgs.sayIn(lang, "slash.set.source", nil),
		"slash.set.source.path":           msgs.sayIn(lang, "slash.set.source.path", nil),
		"slash.session":                   msgs.sayIn(lang, "slash.session", nil),
		"slash.session.create":            msgs.sayIn(lang, "slash.session.create", nil),
		"slash.session.create.name":       msgs.sayIn(lang, "slash.session.create.name", nil),
		"slash.session.create.cmd":        msgs.sayIn(lang, "slash.session.create.cmd", nil),
		"slash.session.create.shared":     msgs.sayIn(lang, "slash.session.create.shared", nil),
		"slash.session.create.backend":    msgs.sayIn(lang, "slash.session.create.backend", nil),
		"slash.session.create.project":    msgs.sayIn(lang, "slash.session.create.project", nil),
		"slash.session.create.clone":      msgs.sayIn(lang, "slash.session.create.clone", nil),
		"slash.session.close":             msgs.sayIn(lang, "slash.session.close", nil),
		"slash.session.close.name":        msgs.sayIn(lang, "slash.session.close.name", nil),
		"slash.session.close.force":       msgs.sayIn(lang, "slash.session.close.force", nil),
		"slash.session.list":              msgs.sayIn(lang, "slash.session.list", nil),
		"slash.session.who":               msgs.sayIn(lang, "slash.session.who", nil),
		"slash.session.who.name":          msgs.sayIn(lang, "slash.session.who.name", nil),
		"slash.session.allow":             msgs.sayIn(lang, "slash.session.allow", nil),
		"slash.session.allow.add":         msgs.sayIn(lang, "slash.session.allow.add", nil),
		"slash.session.allow.add.name":    msgs.sayIn(lang, "slash.session.allow.add.name", nil),
		"slash.session.allow.add.user":    msgs.sayIn(lang, "slash.session.allow.add.user", nil),
		"slash.session.allow.remove":      msgs.sayIn(lang, "slash.session.allow.remove", nil),
		"slash.session.allow.remove.name": msgs.sayIn(lang, "slash.session.allow.remove.name", nil),
		"slash.session.allow.remove.user": msgs.sayIn(lang, "slash.session.allow.remove.user", nil),
		"slash.session.allow.list":        msgs.sayIn(lang, "slash.session.allow.list", nil),
		"slash.session.allow.list.name":   msgs.sayIn(lang, "slash.session.allow.list.name", nil),
		"slash.service":                   msgs.sayIn(lang, "slash.service", nil),
		"slash.service.restart":           msgs.sayIn(lang, "slash.service.restart", nil),
		"slash.service.update":            msgs.sayIn(lang, "slash.service.update", nil),
		"slash.service.update.no_pull":    msgs.sayIn(lang, "slash.service.update.no_pull", nil),
		"slash.stop":                      msgs.sayIn(lang, "slash.stop", nil),
		"slash.verbosity":                 msgs.sayIn(lang, "slash.verbosity", nil),
		"slash.verbosity.level":           msgs.sayIn(lang, "slash.verbosity.level", nil),
		"slash.verbosity.level.silent":    msgs.sayIn(lang, "slash.verbosity.level.silent", nil),
		"slash.verbosity.level.quiet":     msgs.sayIn(lang, "slash.verbosity.level.quiet", nil),
		"slash.verbosity.level.actions":   msgs.sayIn(lang, "slash.verbosity.level.actions", nil),
		"slash.verbosity.level.full":      msgs.sayIn(lang, "slash.verbosity.level.full", nil),
		"slash.mode":                      msgs.sayIn(lang, "slash.mode", nil),
		"slash.mode.mode":                 msgs.sayIn(lang, "slash.mode.mode", nil),
		"slash.mode.mode.normal":          msgs.sayIn(lang, "slash.mode.mode.normal", nil),
		"slash.mode.mode.support":         msgs.sayIn(lang, "slash.mode.mode.support", nil),
		"slash.mode.mode.ambient":         msgs.sayIn(lang, "slash.mode.mode.ambient", nil),
		"slash.mode.mode.off":             msgs.sayIn(lang, "slash.mode.mode.off", nil),
		"slash.allow":                     msgs.sayIn(lang, "slash.allow", nil),
		"slash.allow.add":                 msgs.sayIn(lang, "slash.allow.add", nil),
		"slash.allow.add.user":            msgs.sayIn(lang, "slash.allow.add.user", nil),
		"slash.allow.remove":              msgs.sayIn(lang, "slash.allow.remove", nil),
		"slash.allow.remove.user":         msgs.sayIn(lang, "slash.allow.remove.user", nil),
		"slash.allow.list":                msgs.sayIn(lang, "slash.allow.list", nil),
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
	return lab.command("set", "slash.set").
		Perms(dctl.PermManageGuild).
		With(
			lab.sub("home", "slash.set.home",
				lab.channel("channel", "slash.set.home.channel", true)),
			lab.sub("source", "slash.set.source",
				lab.str("path", "slash.set.source.path", true)),
		)
}

func commandSession(lab labelSet) *dctl.Command {
	return lab.command("session", "slash.session").
		Perms(dctl.PermManageGuild).
		With(
			lab.sub("create", "slash.session.create",
				lab.str("name", "slash.session.create.name", true),
				lab.str("cmd", "slash.session.create.cmd", false),
				lab.boolean("shared", "slash.session.create.shared", false),
				lab.str("backend", "slash.session.create.backend", false).
					Choices(dctl.NewChoice("stream", "stream"), dctl.NewChoice("oneshot", "oneshot")),
				lab.str("project", "slash.session.create.project", false),
				lab.str("clone", "slash.session.create.clone", false),
			),
			lab.sub("close", "slash.session.close",
				lab.str("name", "slash.session.close.name", true).Autocomplete(),
				lab.boolean("force", "slash.session.close.force", false),
			),
			lab.sub("list", "slash.session.list"),
			lab.sub("who", "slash.session.who",
				lab.str("name", "slash.session.who.name", true).Autocomplete()),
			lab.group("allow", "slash.session.allow",
				lab.sub("add", "slash.session.allow.add",
					lab.str("name", "slash.session.allow.add.name", true).Autocomplete(),
					lab.user("slash.session.allow.add.user")),
				lab.sub("remove", "slash.session.allow.remove",
					lab.str("name", "slash.session.allow.remove.name", true).Autocomplete(),
					lab.user("slash.session.allow.remove.user")),
				lab.sub("list", "slash.session.allow.list",
					lab.str("name", "slash.session.allow.list.name", true).Autocomplete()),
			),
		)
}

func commandService(lab labelSet) *dctl.Command {
	return lab.command("service", "slash.service").
		Perms(dctl.PermManageGuild).
		With(
			lab.sub("restart", "slash.service.restart"),
			lab.sub("update", "slash.service.update",
				lab.boolean("no_pull", "slash.service.update.no_pull", false)),
		)
}

func commandStop(lab labelSet) *dctl.Command {
	return lab.command("stop", "slash.stop").
		Perms(dctl.PermManageGuild)
}

func commandVerbosity(lab labelSet) *dctl.Command {
	return lab.command("verbosity", "slash.verbosity").
		Perms(dctl.PermManageGuild).
		With(
			lab.str("level", "slash.verbosity.level", true).
				Choices(
					lab.choice(levelSilent, "slash.verbosity.level.silent"),
					lab.choice(levelQuiet, "slash.verbosity.level.quiet"),
					lab.choice(levelActions, "slash.verbosity.level.actions"),
					lab.choice(levelFull, "slash.verbosity.level.full"),
				),
		)
}

func commandMode(lab labelSet) *dctl.Command {
	return lab.command("mode", "slash.mode").
		Perms(dctl.PermManageGuild).
		With(
			lab.str("mode", "slash.mode.mode", true).
				Choices(
					lab.choice(normalMode, "slash.mode.mode.normal"),
					lab.choice(supportMode, "slash.mode.mode.support"),
					lab.choice(ambientMode, "slash.mode.mode.ambient"),
					lab.choice(offMode, "slash.mode.mode.off"),
				),
		)
}

func commandAllow(lab labelSet) *dctl.Command {
	return lab.command("allow", "slash.allow").
		Perms(dctl.PermManageGuild).
		With(
			lab.sub("add", "slash.allow.add", lab.user("slash.allow.add.user")),
			lab.sub("remove", "slash.allow.remove", lab.user("slash.allow.remove.user")),
			lab.sub("list", "slash.allow.list"),
		)
}
