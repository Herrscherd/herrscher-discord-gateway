package discord

import (
	"testing"

	"github.com/Herrscherd/dctl"
)

func guildMsg(author string, roles ...string) messageCreate {
	m := messageCreate{
		ID: "m1", ChannelID: "c1", GuildID: "g1", Content: "salut",
		Author: dctl.Author{ID: author, Username: author},
	}
	if len(roles) > 0 {
		m.Member = &guildMember{Roles: roles}
	}
	return m
}

type spec struct {
	owner, selfID, bots    string
	users, roles, channels idSet
	ignored                idSet
	allowAll               bool
}

func policy(s spec) *accessPolicy {
	or := func(v idSet) idSet {
		if v == nil {
			return idSet{}
		}
		return v
	}
	return &accessPolicy{
		owner:    s.owner,
		selfID:   s.selfID,
		bots:     s.bots,
		users:    or(s.users),
		roles:    or(s.roles),
		channels: or(s.channels),
		ignored:  or(s.ignored),
		allowAll: s.allowAll,
	}
}

func TestNothingConfiguredAuthorizesNobody(t *testing.T) {
	p := policy(spec{selfID: "app1"})
	if p.permits(guildMsg("u1"), "", true) {
		t.Fatal("a gateway with no allowlist obeyed a stranger")
	}
}

func TestOwnerIsAlwaysAuthorized(t *testing.T) {
	p := policy(spec{selfID: "app1", owner: "owner1"})
	if !p.permits(guildMsg("owner1"), "", true) {
		t.Fatal("the owner was refused")
	}
	if p.permits(guildMsg("u1"), "", true) {
		t.Fatal("an owner-only gateway obeyed someone else")
	}
}

func TestAllowedUsersWildcardOpensToEveryone(t *testing.T) {
	p := policy(spec{selfID: "app1", users: newIDSet("*")})
	if !p.permits(guildMsg("u1"), "", true) {
		t.Fatal("the wildcard refused a user")
	}
}

func TestAllowedRolesAuthorizeInGuildsOnly(t *testing.T) {
	p := policy(spec{selfID: "app1", roles: newIDSet("r1, r2")})
	if !p.permits(guildMsg("u1", "r2"), "", true) {
		t.Fatal("a member of an allowed role was refused")
	}
	if p.permits(guildMsg("u1", "r9"), "", true) {
		t.Fatal("a member of no allowed role was obeyed")
	}
	dm := guildMsg("u1", "r1")
	dm.GuildID = ""
	if p.permits(dm, "", true) {
		t.Fatal("a role carried into a DM, where the guild that granted it does not apply")
	}
}

func TestIgnoredChannelBeatsEveryGrant(t *testing.T) {
	p := policy(spec{selfID: "app1", owner: "owner1", ignored: newIDSet("c1")})
	if p.permits(guildMsg("owner1"), "", true) {
		t.Fatal("an ignored channel answered its owner")
	}
}

func TestIgnoredChannelCoversThreadsOfIt(t *testing.T) {
	p := policy(spec{selfID: "app1", owner: "owner1", ignored: newIDSet("c1")})
	m := guildMsg("owner1")
	m.ChannelID = "t1"
	if p.permits(m, "c1", true) {
		t.Fatal("a thread escaped the channel it hangs off being ignored")
	}
}

func TestAllowedChannelsConfineTheBot(t *testing.T) {
	p := policy(spec{selfID: "app1", owner: "owner1", channels: newIDSet("c9")})
	if p.permits(guildMsg("owner1"), "", true) {
		t.Fatal("the bot answered outside the channels it was confined to")
	}
}

func TestChannelsAloneAuthorizeTheirParticipants(t *testing.T) {
	p := policy(spec{selfID: "app1", channels: newIDSet("c1")})
	if !p.permits(guildMsg("u1"), "", true) {
		t.Fatal("a channel allowlist with no user list refused the room it names")
	}
}

func TestChannelsDoNotAuthorizeWhenUsersAreListed(t *testing.T) {
	p := policy(spec{selfID: "app1", channels: newIDSet("c1"), users: newIDSet("u9")})
	if p.permits(guildMsg("u1"), "", true) {
		t.Fatal("being in an allowed channel bypassed the user allowlist")
	}
}

func TestAllowAllUsersIsAnExplicitOptIn(t *testing.T) {
	p := policy(spec{selfID: "app1", allowAll: true})
	if !p.permits(guildMsg("u1"), "", true) {
		t.Fatal("allow-all refused a user")
	}
}

func TestTheBotNeverObeysItself(t *testing.T) {
	p := policy(spec{selfID: "app1", users: newIDSet("*"), bots: botsAll})
	if p.permits(guildMsg("app1"), "", true) {
		t.Fatal("the bot answered its own message")
	}
}

func TestBotsAreRefusedByDefault(t *testing.T) {
	p := policy(spec{selfID: "app1", users: newIDSet("*")})
	m := guildMsg("bot2")
	m.Author.Bot = true
	if p.permits(m, "", true) {
		t.Fatal("another bot made this one act without being asked for")
	}
}

func TestBotsOnMentionsNeedTheMention(t *testing.T) {
	p := policy(spec{selfID: "app1", users: newIDSet("*"), bots: botsMentions})
	m := guildMsg("bot2")
	m.Author.Bot = true
	if !p.permits(m, "", true) {
		t.Fatal("a mentioning bot was refused under the mentions policy")
	}
	if p.permits(m, "", false) {
		t.Fatal("a bot that did not mention us was obeyed under the mentions policy")
	}
}
