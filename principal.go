package discord

import "github.com/Herrscherd/dctl"

const principalPrefix = "discord:"

func principalOf(ix dctl.Interaction) string {
	userID := ix.UserID()
	if userID == "" {
		return ""
	}
	return principalPrefix + userID
}
