package discord

import "strings"

// The gateway routes two kinds of select-menu click, told apart by the prefix of
// their custom_id. Neither prefix is a prefix of the other, so a click can never
// take the wrong route.
const (
	choiceCustomIDPrefix = "dctlchoice:"
	bindCustomIDPrefix   = "dctlbind:"
)

// ChoiceCustomID builds the custom_id carried by a session's choice select menu.
func ChoiceCustomID(session string) string { return choiceCustomIDPrefix + session }

// ParseChoiceCustomID extracts the session name from a choice-menu custom_id and
// reports whether the id is a choice menu at all.
func ParseChoiceCustomID(id string) (string, bool) {
	return strings.CutPrefix(id, choiceCustomIDPrefix)
}

// BindCustomID builds the custom_id carried by the menu that asks which repo a
// channel should work on.
func BindCustomID(channel string) string { return bindCustomIDPrefix + channel }

// ParseBindCustomID extracts the channel id from a repo-binding custom_id and
// reports whether the id is a binding menu at all.
func ParseBindCustomID(id string) (string, bool) {
	return strings.CutPrefix(id, bindCustomIDPrefix)
}
