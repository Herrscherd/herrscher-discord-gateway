package discord

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestForkingIntoAThreadSaysWhereTheWorkWent(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	c.nextThreadID = "t1"

	r.onMessage(context.Background(), ownerPing("ouvre un thread privé pour herrscher"))

	var said bool
	for _, s := range c.sent {
		if s.channel == "c1" && strings.Contains(s.content, "<#t1>") {
			said = true
		}
	}
	if !said {
		t.Fatalf("sent = %+v, want a line in c1 linking the thread", c.sent)
	}
}

func TestThreadRequestOpensAPrivateThreadAndWorksThere(t *testing.T) {
	r, ctrl, c, f := newTestRouterRendering(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	c.nextThreadID = "t1"

	r.onMessage(context.Background(), ownerPing("ouvre un thread et corrige herrscher"))

	if len(c.threads) != 1 || c.threads[0].channel != "c1" {
		t.Fatalf("threads = %+v, want one opened in the pinged channel", c.threads)
	}
	if len(c.members) != 1 || c.members[0] != (outMsg{"t1", "owner1"}) {
		t.Fatalf("members = %+v, want the owner added to t1", c.members)
	}
	spec := ctrl.created[0]
	if spec.ChannelID != "t1" || spec.Name != sessionNameFor("t1") {
		t.Fatalf("spec = %+v, want the session to live in the thread", spec)
	}
	if in := ctrl.submitted[spec.Name][0]; in.Conversation.ID != "t1" {
		t.Fatalf("conversation = %+v, want the answer to land in the thread", in.Conversation)
	}
	if r.binds.Session("t1") != spec.Name || !r.binds.IsThread("t1") {
		t.Fatal("the thread was not remembered as a bound conversation of ours")
	}
	if got := r.sinks.at("t1").lastUser; got != (msgRef{ch: "c1", id: "m1", author: "owner1"}) {
		t.Fatalf("lastUser = %+v, want the ping in its own channel", got)
	}
	if len(f.reacted) != 1 || f.reacted[0] != ackEmoji {
		t.Fatalf("reacted = %v, want the ping acked", f.reacted)
	}
}

func TestThreadInheritsTheChannelRenderLevel(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	c.nextThreadID = "t1"
	if err := r.binds.SetLevel("c1", levelFull); err != nil {
		t.Fatal(err)
	}

	r.onMessage(context.Background(), ownerPing("ouvre un thread et corrige herrscher"))

	if got := r.binds.Level("t1"); got != levelFull {
		t.Fatalf("level(t1) = %q, want %q, the thread went quiet on its own", got, levelFull)
	}
}

func TestThreadMessagesNeedNoMention(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	ctrl.live["ch-t1"] = true
	if err := r.binds.BindThread("t1", "t1", "c1", "ch-t1"); err != nil {
		t.Fatal(err)
	}
	r.onMessage(context.Background(), messageCreate{
		ID: "m2", ChannelID: "t1", Content: "et les tests ?",
		Author: dctl.Author{ID: "owner1", Username: "leo"},
	})
	if got := ctrl.submitted["ch-t1"]; len(got) != 1 {
		t.Fatalf("submitted = %+v, want the bare message to open a turn", got)
	}
}

func TestBlankedThreadMessageIsReadBack(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.live["ch-t1"] = true
	if err := r.binds.BindThread("t1", "t1", "c1", "ch-t1"); err != nil {
		t.Fatal(err)
	}
	c.read = []dctl.Message{{ID: "m2", ChannelID: "t1", Content: "et les tests ?"}}

	r.onMessage(context.Background(), messageCreate{
		ID: "m2", ChannelID: "t1",
		Author: dctl.Author{ID: "owner1", Username: "leo"},
	})

	got := ctrl.submitted["ch-t1"]
	if len(got) != 1 || !strings.Contains(got[0].Text, "et les tests ?") {
		t.Fatalf("submitted = %+v, want the turn to carry the message body", got)
	}
}

func TestThreadFailureFallsBackToTheChannelOutLoud(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	c.threadErr = errors.New("missing permission")

	r.onMessage(context.Background(), ownerPing("en privé stp, herrscher"))

	if len(c.sent) != 1 || c.sent[0].channel != "c1" || !strings.Contains(c.sent[0].content, "private thread") {
		t.Fatalf("sent = %+v, want the fallback said out loud in the channel", c.sent)
	}
	if spec := ctrl.created[0]; spec.ChannelID != "c1" {
		t.Fatalf("spec = %+v, want the job to fall back to the channel", spec)
	}
}

func TestThreadWithoutItsMemberIsNotUsed(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	c.memberErr = errors.New("forbidden")

	r.onMessage(context.Background(), ownerPing("dans un fil, herrscher"))

	if spec := ctrl.created[0]; spec.ChannelID != "c1" {
		t.Fatalf("spec = %+v, want the channel rather than a thread only the bot can read", spec)
	}
}

func TestThreadWithoutItsMemberIsDeleted(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	c.memberErr = errors.New("forbidden")

	r.onMessage(context.Background(), ownerPing("dans un fil, herrscher"))

	if !slices.Equal(c.deletedChannels, []string{"thread1"}) {
		t.Fatalf("deleted channels = %v, want the orphan thread removed", c.deletedChannels)
	}
}

func TestOrphanThreadSurvivesAFailedDelete(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	var logs bytes.Buffer
	r.log = bufferLog(&logs)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	c.memberErr = errors.New("forbidden")
	c.deleteChanErr = errors.New("missing permission")

	r.onMessage(context.Background(), ownerPing("dans un fil, herrscher"))

	if !slices.Equal(c.deletedChannels, []string{"thread1"}) {
		t.Fatalf("deleted channels = %v, want the delete attempted", c.deletedChannels)
	}
	if spec := ctrl.created[0]; spec.ChannelID != "c1" {
		t.Fatalf("spec = %+v, want the job to fall back to the channel after a failed delete", spec)
	}
	if record := recordWith(logs.String(), "orphan private thread could not be deleted"); !strings.Contains(record, "thread=thread1") {
		t.Fatalf("the orphan warning is %q, want it to name thread1", record)
	}
}

func TestRebindingInsideAThreadStaysInIt(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	if err := r.binds.BindThread("t1", "t1", "c1", "ch-t1"); err != nil {
		t.Fatal(err)
	}
	r.onMessage(context.Background(), messageCreate{
		ID: "m2", ChannelID: "t1", Content: "reprends herrscher",
		Author: dctl.Author{ID: "owner1", Username: "leo"},
	})

	if len(c.threads) != 0 {
		t.Fatalf("threads = %+v, want no thread opened inside a thread", c.threads)
	}
	if spec := ctrl.created[0]; spec.ChannelID != "t1" {
		t.Fatalf("spec = %+v, want the job to stay in the thread", spec)
	}
	if !r.binds.IsThread("t1") {
		t.Fatal("the thread lost its no-@mention rule when its session died")
	}
}

func TestThreadRequestInABoundChannelForksIntoOne(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.live["ch-c1"] = true
	ctrl.sessions = []contracts.SessionInfo{{Name: "ch-c1", ChannelID: "c1", Project: "enderbot"}}
	c.nextThreadID = "t1"
	if err := r.binds.Bind("c1", "ch-c1"); err != nil {
		t.Fatal(err)
	}

	r.onMessage(context.Background(), ownerPing("ouvre un thread et regarde ce bug (fil privé)"))

	if len(c.threads) != 1 || c.threads[0].channel != "c1" {
		t.Fatalf("threads = %+v, want one opened in the pinged channel", c.threads)
	}
	if len(c.members) != 1 || c.members[0] != (outMsg{"t1", "owner1"}) {
		t.Fatalf("members = %+v, want the owner added to t1", c.members)
	}
	if len(ctrl.created) != 1 {
		t.Fatalf("created = %+v, want one session for the thread", ctrl.created)
	}
	spec := ctrl.created[0]
	if spec.ChannelID != "t1" || spec.Name != sessionNameFor("t1") || spec.Project != "enderbot" {
		t.Fatalf("spec = %+v, want a session in t1 on the channel's own repo", spec)
	}
	if len(c.menus) != 0 {
		t.Fatalf("menus = %+v, want the repo inherited rather than re-asked", c.menus)
	}
	if got := ctrl.submitted["ch-c1"]; len(got) != 0 {
		t.Fatalf("submitted to the channel session = %+v, want the ping to go to the thread only", got)
	}
	if in := ctrl.submitted[spec.Name]; len(in) != 1 || in[0].Conversation.ID != "t1" {
		t.Fatalf("submitted = %+v, want the ping replayed into the thread", in)
	}
	if r.binds.Session("t1") != spec.Name || !r.binds.IsThread("t1") {
		t.Fatal("the thread was not remembered as a bound conversation of ours")
	}
}

func TestForkFallsBackToTheBoundSessionWhenTheThreadFails(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.live["ch-c1"] = true
	ctrl.sessions = []contracts.SessionInfo{{Name: "ch-c1", ChannelID: "c1", Project: "enderbot"}}
	c.threadErr = errors.New("missing permission")
	if err := r.binds.Bind("c1", "ch-c1"); err != nil {
		t.Fatal(err)
	}

	r.onMessage(context.Background(), ownerPing("dans un fil privé, ce bug"))

	if len(ctrl.created) != 0 {
		t.Fatalf("created = %+v, want no second session on a channel that already has one", ctrl.created)
	}
	if got := ctrl.submitted["ch-c1"]; len(got) != 1 {
		t.Fatalf("submitted = %+v, want the ping answered by the bound session", got)
	}
	if len(c.sent) != 1 || !strings.Contains(c.sent[0].content, "private thread") {
		t.Fatalf("sent = %+v, want the fallback said out loud", c.sent)
	}
}

func TestThreadWordInsideAThreadDoesNotForkAgain(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.live["ch-t1"] = true
	ctrl.sessions = []contracts.SessionInfo{{Name: "ch-t1", ChannelID: "t1", Project: "enderbot"}}
	c.nextThreadID = "t2"
	if err := r.binds.BindThread("t1", "t1", "c1", "ch-t1"); err != nil {
		t.Fatal(err)
	}

	r.onMessage(context.Background(), messageCreate{
		ID: "m2", ChannelID: "t1", Content: "reprends le fil d'hier",
		Author: dctl.Author{ID: "owner1", Username: "leo"},
	})

	if len(c.threads) != 0 {
		t.Fatalf("threads = %+v, want no thread opened inside a thread", c.threads)
	}
	if got := ctrl.submitted["ch-t1"]; len(got) != 1 {
		t.Fatalf("submitted = %+v, want the message answered in the thread it was written in", got)
	}
}

func TestASupportChannelOpensAThreadPerPing(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	if err := r.binds.SetMode("c1", supportMode); err != nil {
		t.Fatal(err)
	}

	r.onMessage(context.Background(), supportPing("m1", "corrige herrscher"))
	r.onMessage(context.Background(), supportPing("m2", "et herrscher plante au boot"))

	if len(c.public) != 2 || c.public[0].channel != "m1" || c.public[1].channel != "m2" {
		t.Fatalf("public = %+v, want one thread hanging off each ping", c.public)
	}
	if len(ctrl.created) != 2 || ctrl.created[0].ChannelID == ctrl.created[1].ChannelID {
		t.Fatalf("created = %+v, want two sessions in two conversations", ctrl.created)
	}
	if got := r.binds.Session("c1"); got != "" {
		t.Fatalf("session(c1) = %q, a support channel must stay unbound, or the second ping lands on the first job", got)
	}
	for _, spec := range ctrl.created {
		if r.binds.Session(spec.ChannelID) != spec.Name || !r.binds.IsThread(spec.ChannelID) {
			t.Fatalf("%q was not remembered as a thread of ours", spec.ChannelID)
		}
	}
}

func TestTheSupportRepoIsAskedOnceForTheRoom(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	if err := r.binds.SetMode("c1", supportMode); err != nil {
		t.Fatal(err)
	}

	r.onMessage(context.Background(), supportPing("m1", "corrige herrscher"))
	r.onMessage(context.Background(), supportPing("m2", "ça plante au boot"))

	if len(c.menus) != 0 {
		t.Fatalf("menus = %+v, want the named repo to answer for the room", c.menus)
	}
	if len(ctrl.created) != 2 || ctrl.created[1].Project != "herrscher" {
		t.Fatalf("created = %+v, want the second ticket on the repo the first one settled", ctrl.created)
	}
}

func TestASupportThreadThatCannotOpenAnswersInTheChannelOutLoud(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	c.publicErr = errors.New("403: Missing Permissions")
	if err := r.binds.SetMode("c1", supportMode); err != nil {
		t.Fatal(err)
	}

	r.onMessage(context.Background(), supportPing("m1", "corrige herrscher"))

	if len(ctrl.created) != 1 || ctrl.created[0].ChannelID != "c1" {
		t.Fatalf("created = %+v, want the job to happen in the channel", ctrl.created)
	}
	if len(c.sent) == 0 || !strings.Contains(c.sent[0].content, "thread") {
		t.Fatalf("sent = %+v, want the fallback said out loud", c.sent)
	}
}

func TestAPingInsideASupportThreadStaysThere(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	if err := r.binds.SetMode("c1", supportMode); err != nil {
		t.Fatal(err)
	}
	r.onMessage(context.Background(), supportPing("m1", "corrige herrscher"))
	thread := ctrl.created[0].ChannelID
	opened := len(c.public)

	r.onMessage(context.Background(), messageCreate{
		ID: "m2", ChannelID: thread, Content: "et aussi le login",
		Author: dctl.Author{ID: "owner1", Username: "leo"},
	})

	if len(c.public) != opened {
		t.Fatalf("public = %+v, want no thread opened inside a thread", c.public)
	}
	if got := ctrl.submitted[ctrl.created[0].Name]; len(got) != 2 {
		t.Fatalf("submitted = %d, want the follow-up answered by the ticket's own session", len(got))
	}
}

func TestASupportThreadInheritsTheChannelRenderLevel(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	if err := r.binds.SetMode("c1", supportMode); err != nil {
		t.Fatal(err)
	}
	if err := r.binds.SetLevel("c1", levelSilent); err != nil {
		t.Fatal(err)
	}

	r.onMessage(context.Background(), supportPing("m1", "corrige herrscher"))

	if got := r.binds.Level(ctrl.created[0].ChannelID); got != levelSilent {
		t.Fatalf("level = %q, want %q carried into the ticket", got, levelSilent)
	}
}

func supportPing(id, text string) messageCreate {
	m := ownerPing(text)
	m.ID = id
	return m
}
