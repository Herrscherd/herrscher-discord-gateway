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

func TestChoicePickGoesToTheSessionOfWhoeverClicked(t *testing.T) {
	r, ctrl, _ := newGuildRouter(t, nil)
	r.binds.SetLastRepo("local:herrscher")
	r.onMessage(context.Background(), guildPing("u1", "fix the login bug", true))
	r.onMessage(context.Background(), guildPing("u2", "and the signup one", true))
	for _, si := range ctrl.Sessions() {
		ctrl.live[si.Name] = true
	}

	if msg := r.onChoicePick(context.Background(), "c1", "u2", false, "yes"); msg != "" {
		t.Fatalf("onChoicePick = %q, want the pick to land", msg)
	}
	mine := r.binds.Session(r.cfg.scope.slot(chatChannel, "c1", "u2"))
	if mine == "" {
		t.Fatal("u2 holds no session in c1")
	}
	if got := ctrl.picked[mine]; len(got) != 1 || got[0] != "yes" {
		t.Fatalf("picked = %v, want u2's own session to have received the answer", ctrl.picked)
	}
}

func TestRepoMenuOnlyAnswersTheOneWhoAsked(t *testing.T) {
	r, ctrl, _ := newGuildRouter(t, nil)
	r.onMessage(context.Background(), guildPing("u1", "fix the login bug", true))

	if msg := r.onBindPick(context.Background(), "c1", "u2", "local:herrscher"); !strings.Contains(msg, "u1") {
		t.Fatalf("onBindPick = %q, want it to point back at who asked", msg)
	}
	if len(ctrl.created) != 0 {
		t.Fatalf("created = %+v, want a stranger's click to open nothing", ctrl.created)
	}
	if msg := r.onBindPick(context.Background(), "c1", "u1", "local:herrscher"); strings.Contains(msg, "u1") {
		t.Fatalf("onBindPick = %q, want the asker's own click to go through", msg)
	}
	if len(ctrl.created) != 1 {
		t.Fatalf("created = %+v, want the asker's click to open the session", ctrl.created)
	}
}

func TestSwitchingModeUnbindsEveryoneInTheRoom(t *testing.T) {
	r, ctrl, _ := newGuildRouter(t, nil)
	r.binds.SetLastRepo("local:herrscher")
	r.onMessage(context.Background(), guildPing("u1", "fix the login bug", true))
	r.onMessage(context.Background(), guildPing("u2", "and the signup one", true))
	if got := r.binds.SessionsIn("c1"); len(got) != 2 {
		t.Fatalf("SessionsIn = %v, want one per participant", got)
	}

	s := &slash{ctx: context.Background(), binds: r.binds, router: r, ctrl: ctrl}
	s.setMode("c1", supportMode)

	if got := r.binds.SessionsIn("c1"); len(got) != 0 {
		t.Fatalf("SessionsIn = %v, want the room unbound so the new mode routes fresh", got)
	}
}

func TestRepoMenuOutlivingItsPendingPingStaysOwned(t *testing.T) {
	cases := []struct {
		name       string
		clicker    string
		wantCreate int
	}{
		{"stranger clicks the orphaned menu", "u2", 0},
		{"an authorized user clicks it", "u1", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, ctrl, _ := newGuildRouter(t, func(cfg *routerConfig) {
				cfg.access = accessSettings{users: newIDSet("u1")}
			})
			r.onMessage(context.Background(), guildPing("u1", "fix the login bug", true))
			r.mu.Lock()
			r.pending, r.jobs = map[string]messageCreate{}, map[string]job{}
			r.mu.Unlock()

			r.onBindPick(context.Background(), "c1", tc.clicker, "local:herrscher")

			if len(ctrl.created) != tc.wantCreate {
				t.Fatalf("created = %+v, want %d session(s)", ctrl.created, tc.wantCreate)
			}
		})
	}
}

func TestAnOrphanedMenuAnsweredInAThreadKeepsTheThread(t *testing.T) {
	r, ctrl, _ := newGuildRouter(t, func(cfg *routerConfig) {
		cfg.access = accessSettings{users: newIDSet("u1")}
	})
	if err := r.binds.SetMode("c1", supportMode); err != nil {
		t.Fatal(err)
	}
	r.onMessage(context.Background(), guildPing("u1", "fix the login bug", true))
	thread := ""
	for conv := range r.jobs {
		thread = conv
	}
	if thread == "" || thread == "c1" {
		t.Fatalf("thread = %q, want the menu posted in a thread of its own", thread)
	}
	if !r.binds.IsThread(thread) {
		t.Fatal("the thread the menu was posted in is not registered as one")
	}
	r.mu.Lock()
	r.pending, r.jobs = map[string]messageCreate{}, map[string]job{}
	r.mu.Unlock()

	r.onBindPick(context.Background(), thread, "u1", "local:herrscher")

	if len(ctrl.created) != 1 {
		t.Fatalf("created = %+v, want the click to open the session", ctrl.created)
	}
	if got := r.binds.Parent(thread); got != "c1" {
		t.Fatalf("Parent(%s) = %q, want c1", thread, got)
	}
	if !r.binds.IsThread(thread) {
		t.Fatal("the binding lost the thread flag")
	}
}
