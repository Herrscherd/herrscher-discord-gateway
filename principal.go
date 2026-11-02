package discord

import "github.com/Herrscherd/dctl"

const principalPrefix = "discord:"

func principalOf(ix dctl.Interaction) string {
	id := ix.UserID()
	if id == "" {
		return ""
	}
	return principalPrefix + id
}
