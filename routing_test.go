package discord

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

func newGuildRouter(t *testing.T, tune func(*routerConfig)) (*router, *fakeCtrl, *fakeClient) {
	t.Helper()
	ctrl, c := newFakeCtrl(), &fakeClient{}
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	binds := newBindStore(filepath.Join(t.TempDir(), "router.json"))
	cfg := routerConfig{
		appID:           "app1",
		contextMessages: 0,
		access:          accessSettings{users: newIDSet("*")},
		address:         defaultAddress("app1"),
		scope:           defaultScope(),
	}
	if tune != nil {
		tune(&cfg)
	}
	r := newRouter(func() contracts.SessionControl { return ctrl }, c, binds,
		newSinks(context.Background(), &fakeRender{}, staticLevels("silent"), "", false), cfg)
	return r, ctrl, c
}

func guildPing(author, text string, mention bool) messageCreate {
	m := messageCreate{
		ID: "m-" + author, ChannelID: "c1", GuildID: "g1", Content: text,
		Author: dctl.Author{ID: author, Username: author},
	}
	if mention {
		m.Mentions = []dctl.Author{{ID: "app1"}}
	}
	return m
}

func TestTwoPeopleInAChannelGetTwoSessions(t *testing.T) {
	r, ctrl, _ := newGuildRouter(t, nil)
	r.binds.SetLastRepo("local:herrscher")

	r.onMessage(context.Background(), guildPing("u1", "fix the login bug", true))
	r.onMessage(context.Background(), guildPing("u2", "and the signup one", true))

	if len(ctrl.created) != 2 {
		t.Fatalf("created = %+v, want one session per person", ctrl.created)
	}
	if ctrl.created[0].Name == ctrl.created[1].Name {
		t.Fatalf("both people landed in %q", ctrl.created[0].Name)
	}
	for _, s := range ctrl.created {
		if s.ChannelID != "c1" {
			t.Fatalf("session %q answers in %q, want the channel it was asked in", s.Name, s.ChannelID)
		}
	}
}

func TestOnePersonKeepsOneSessionAcrossTurns(t *testing.T) {
	r, ctrl, _ := newGuildRouter(t, nil)
	r.binds.SetLastRepo("local:herrscher")

	r.onMessage(context.Background(), guildPing("u1", "fix the login bug", true))
	r.onMessage(context.Background(), guildPing("u1", "now the tests", true))

	if len(ctrl.created) != 1 {
		t.Fatalf("created = %+v, want one session reused", ctrl.created)
	}
	if got := ctrl.submitted[ctrl.created[0].Name]; len(got) != 2 {
		t.Fatalf("submitted %d turns, want 2 into the same session", len(got))
	}
}

func TestASharedChannelPutsEveryoneInOneSession(t *testing.T) {
	r, ctrl, _ := newGuildRouter(t, func(c *routerConfig) { c.scope.groupPerUser = false })
	r.binds.SetLastRepo("local:herrscher")

	r.onMessage(context.Background(), guildPing("u1", "fix the login bug", true))
	r.onMessage(context.Background(), guildPing("u2", "and the signup one", true))

	if len(ctrl.created) != 1 {
		t.Fatalf("created = %+v, want one shared session", ctrl.created)
	}
	turns := ctrl.submitted[ctrl.created[0].Name]
	if len(turns) != 2 {
		t.Fatalf("submitted %d turns into the shared session, want 2", len(turns))
	}
	if !strings.Contains(turns[0].Text, "Session partagée") {
		t.Fatalf("the opening turn of a shared session did not say so: %q", turns[0].Text)
	}
	for i, want := range []string{"[u1] fix the login bug", "[u2] and the signup one"} {
		if !strings.Contains(turns[i].Text, want) {
			t.Fatalf("turn %d = %q, want it to carry %q", i, turns[i].Text, want)
		}
	}
}

func TestAmbientModeAnswersWithoutBeingMentioned(t *testing.T) {
	r, ctrl, _ := newGuildRouter(t, nil)
	r.binds.SetLastRepo("local:herrscher")
	r.binds.SetMode("c1", ambientMode)

	r.onMessage(context.Background(), guildPing("u1", "quelqu'un sait pourquoi le build casse ?", false))

	if len(ctrl.created) != 1 {
		t.Fatalf("created = %+v, want the bot to have answered an ambient channel", ctrl.created)
	}
}

func TestANormalChannelIgnoresWhatIsNotAddressedToIt(t *testing.T) {
	r, ctrl, _ := newGuildRouter(t, nil)
	r.binds.SetLastRepo("local:herrscher")

	r.onMessage(context.Background(), guildPing("u1", "quelqu'un sait pourquoi le build casse ?", false))

	if len(ctrl.created) != 0 {
		t.Fatalf("created = %+v, want silence: nobody addressed the bot", ctrl.created)
	}
}

func TestOffModeIgnoresEvenAMention(t *testing.T) {
	r, ctrl, _ := newGuildRouter(t, nil)
	r.binds.SetLastRepo("local:herrscher")
	r.binds.SetMode("c1", offMode)

	r.onMessage(context.Background(), guildPing("u1", "fix the login bug", true))

	if len(ctrl.created) != 0 {
		t.Fatalf("created = %+v, want silence: this channel is off", ctrl.created)
	}
}

func TestAnUnauthorizedMentionIsIgnored(t *testing.T) {
	r, ctrl, _ := newGuildRouter(t, func(c *routerConfig) { c.access.users = newIDSet("u9") })
	r.binds.SetLastRepo("local:herrscher")

	r.onMessage(context.Background(), guildPing("u1", "fix the login bug", true))

	if len(ctrl.created) != 0 {
		t.Fatalf("created = %+v, want silence: u1 is not on the allowlist", ctrl.created)
	}
}

func TestTheConfiguredPersonaRunsEverySessionItOpens(t *testing.T) {
	r, ctrl, _ := newGuildRouter(t, func(c *routerConfig) { c.agent = "greeter" })
	r.binds.SetLastRepo("local:herrscher")

	r.onMessage(context.Background(), guildPing("u1", "fix the login bug", true))

	if len(ctrl.created) != 1 || ctrl.created[0].Agent != "greeter" {
		t.Fatalf("created = %+v, want the session to run as the configured persona", ctrl.created)
	}
}

func TestDeletingAChannelClosesEveryonesSession(t *testing.T) {
	r, ctrl, _ := newGuildRouter(t, nil)
	r.binds.SetLastRepo("local:herrscher")
	r.onMessage(context.Background(), guildPing("u1", "fix the login bug", true))
	r.onMessage(context.Background(), guildPing("u2", "and the signup one", true))

	r.onChannelGone(context.Background(), "c1")

	if len(ctrl.closed) != 2 {
		t.Fatalf("closed = %+v, want every session the room held", ctrl.closed)
	}
	if got := r.binds.SessionsIn("c1"); len(got) != 0 {
		t.Fatalf("bindings survived the room: %v", got)
	}
}
