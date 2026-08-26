package discord

import "github.com/Herrscherd/dctl"

// principalPrefix is this gateway's kind: the word the daemon's role table and
// its refusals spell a Discord account with. An operator reads a principal out
// of a refusal and types it straight back into `role grant`, so the spelling
// here is the spelling there, and it is a constant rather than something
// derived so the two cannot drift apart.
const principalPrefix = "discord:"

// principalOf names the human behind an interaction, in the form the daemon
// expects: "discord:1234". The division is deliberate. This gateway says who is
// asking, because Discord is the only side that knows, and the daemon decides
// what that answer permits. Nothing here reads a role or anticipates a refusal.
//
// An interaction with no user id names nobody. Discord carries the caller in
// member.user inside a guild and in user inside a DM, and dctl reads both, so an
// interaction with neither is a malformed payload rather than a real caller.
// Returning "" leaves the decision exactly where it sat before the daemon knew
// about principals, which is what the contract asks of a gateway with no
// identity to offer. Folding every nameless caller into a bare "discord:" would
// be worse than saying nothing: it is a principal an operator could grant a role
// to, and the grant would land on all of them at once.
func principalOf(ix dctl.Interaction) string {
	id := ix.UserID()
	if id == "" {
		return ""
	}
	return principalPrefix + id
}
