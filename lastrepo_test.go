package discord

import (
	"context"
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

// pingIn is ownerPing addressed to a named channel, for the tests that need a
// second conversation somewhere other than c1.
func pingIn(channel, text string) messageCreate {
	m := ownerPing(text)
	m.ChannelID = channel
	m.ID = "m-" + channel
	return m
}

func TestASecondRoomReusesTheLastRepoInsteadOfAsking(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}, {Name: "Herrscherd/dctl"}}

	// The operator answers the question once, in c1.
	r.onMessage(context.Background(), ownerPing("fix the login bug"))
	r.onBindPick(context.Background(), "c1", "", "local:herrscher")

	menusBefore := len(c.menus)
	r.onMessage(context.Background(), pingIn("c2", "tu as accès au skill ?"))

	if len(c.menus) != menusBefore {
		t.Fatalf("menus = %+v, want no new question — the operator already answered it", c.menus)
	}
	if len(ctrl.created) != 2 {
		t.Fatalf("created = %+v, want a second session on the remembered repo", ctrl.created)
	}
	if got := ctrl.created[1]; got.ChannelID != "c2" || got.Project != "herrscher" {
		t.Fatalf("spec = %+v, want c2 adopted on herrscher", got)
	}
}

// A guess that is never stated is a guess the operator finds out about later,
// once the work has already gone to the wrong repo.
func TestReusingTheLastRepoSaysWhichOne(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	r.onMessage(context.Background(), ownerPing("fix the login bug"))
	r.onBindPick(context.Background(), "c1", "", "local:herrscher")

	r.onMessage(context.Background(), pingIn("c2", "une question"))

	var said string
	for _, m := range c.sent {
		if m.channel == "c2" {
			said = m.content
		}
	}
	if !strings.Contains(said, "herrscher") {
		t.Fatalf("said %q in c2, want it to name the repo it picked", said)
	}
	// The menu encoding is an internal detail; "local:herrscher" in a channel is
	// noise the operator has to decode.
	if strings.Contains(said, "local:") {
		t.Errorf("said %q, want the value rendered without its encoding prefix", said)
	}
}

// The ping still replays: reusing the answer must not cost the message that
// prompted it, which is the whole reason the conversation opened.
func TestTheReusedRepoStillReplaysThePing(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	r.onMessage(context.Background(), ownerPing("fix the login bug"))
	r.onBindPick(context.Background(), "c1", "", "local:herrscher")

	r.onMessage(context.Background(), pingIn("c2", "la vraie question"))

	name := ctrl.created[1].Name
	got := ctrl.submitted[name]
	if len(got) != 1 || !strings.Contains(got[0].Text, "la vraie question") {
		t.Fatalf("submitted = %+v, want the ping replayed into the new session", got)
	}
}

func TestTheFirstPingEverStillAsks(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}

	r.onMessage(context.Background(), ownerPing("fix the login bug"))

	if len(c.menus) != 1 {
		t.Fatalf("menus = %+v, want the question asked when nothing is remembered", c.menus)
	}
	if len(ctrl.created) != 0 {
		t.Fatal("a session was created before the operator ever picked a repo")
	}
}

// A repo that has since been renamed or lost must not be bound blind: the
// failure would surface far from here, as a path error.
func TestAVanishedRepoFallsBackToTheQuestion(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	r.onMessage(context.Background(), ownerPing("fix the login bug"))
	r.onBindPick(context.Background(), "c1", "", "local:herrscher")

	ctrl.repos = []contracts.RepoRef{{Name: "autre-chose", Local: true}}
	menusBefore := len(c.menus)
	r.onMessage(context.Background(), pingIn("c2", "une question"))

	if len(c.menus) != menusBefore+1 {
		t.Fatalf("menus = %+v, want the question asked again when the remembered repo is gone", c.menus)
	}
	if len(ctrl.created) != 1 {
		t.Fatalf("created = %+v, want no session bound to a repo that no longer exists", ctrl.created)
	}
}

// A repo named in the ping still wins: the operator saying where beats the
// gateway remembering where.
func TestANamedRepoStillBeatsTheRemembered(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}, {Name: "dctl", Local: true}}
	r.onMessage(context.Background(), ownerPing("fix the login bug"))
	r.onBindPick(context.Background(), "c1", "", "local:herrscher")

	r.onMessage(context.Background(), pingIn("c2", "regarde dctl stp"))

	if got := ctrl.created[1].Project; got != "dctl" {
		t.Fatalf("project = %q, want the repo the ping named", got)
	}
}

func TestLastRepoSurvivesAReload(t *testing.T) {
	path := t.TempDir() + "/router.json"
	s := newBindStore(path)
	if err := s.SetLastRepo("local:herrscher"); err != nil {
		t.Fatalf("SetLastRepo: %v", err)
	}
	// A daemon restart must not cost the answer: being asked again after every
	// restart is the friction this removes.
	if got := newBindStore(path).LastRepo(); got != "local:herrscher" {
		t.Errorf("LastRepo after reload = %q, want %q", got, "local:herrscher")
	}
}
