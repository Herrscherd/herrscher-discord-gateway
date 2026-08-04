package discord

// trigger decides which incoming messages the bot acts on. The rule is
// deliberately narrow and lives entirely here: a turn starts only when the
// configured owner addresses the bot, by @mention or by replying to one of its
// messages. Every other message — including everybody else's — is still read as
// context later, but never makes the bot act.
type trigger struct {
	owner string // Discord user id of the operator the bot obeys
	appID string // the bot's own application (user) id
}

// fires reports whether m should open a turn. An unset owner fires for nobody:
// a misconfigured bot must be inert, never obedient to everyone.
func (t trigger) fires(m messageCreate) bool {
	if t.owner == "" || m.Author.ID != t.owner || m.Author.Bot {
		return false
	}
	for _, u := range m.Mentions {
		if u.ID == t.appID {
			return true
		}
	}
	return m.Referenced != nil && m.Referenced.Author.ID == t.appID
}
