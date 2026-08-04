package discord

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

// fakeCtrl records what the router asked the core to do.
type fakeCtrl struct {
	repos     []contracts.RepoRef
	created   []contracts.CreateSession
	submitted map[string][]contracts.Inbound
	live      map[string]bool
	picked    map[string][]string
}

func newFakeCtrl() *fakeCtrl {
	return &fakeCtrl{
		submitted: map[string][]contracts.Inbound{},
		live:      map[string]bool{},
		picked:    map[string][]string{},
	}
}

func (f *fakeCtrl) Dispatch(context.Context, []string) (string, error) { return "", nil }
func (f *fakeCtrl) Create(_ context.Context, s contracts.CreateSession) (string, error) {
	f.created = append(f.created, s)
	f.live[s.Name] = true
	return "created", nil
}
func (f *fakeCtrl) Close(context.Context, string, bool) (string, error) { return "", nil }
func (f *fakeCtrl) Sessions() []contracts.SessionInfo                   { return nil }
func (f *fakeCtrl) Scrollback(string) []contracts.ScrollbackLine        { return nil }
func (f *fakeCtrl) Resume(string) error                                 { return nil }
func (f *fakeCtrl) Interrupt(string) bool                               { return false }
func (f *fakeCtrl) Submit(name string, in contracts.Inbound) bool {
	if !f.live[name] {
		return false
	}
	f.submitted[name] = append(f.submitted[name], in)
	return true
}
func (f *fakeCtrl) Pick(name, value string) bool {
	if !f.live[name] {
		return false
	}
	f.picked[name] = append(f.picked[name], value)
	return true
}
func (f *fakeCtrl) Repos(context.Context) ([]contracts.RepoRef, error) { return f.repos, nil }

func newTestRouter(t *testing.T) (*router, *fakeCtrl, *fakeClient) {
	t.Helper()
	ctrl, c := newFakeCtrl(), &fakeClient{}
	binds := newBindStore(filepath.Join(t.TempDir(), "router.json"))
	r := newRouter(func() contracts.SessionControl { return ctrl }, c, binds,
		newSinks(context.Background(), &fakeRender{}, "full"),
		routerConfig{owner: "owner1", appID: "app1", contextMessages: 5, playbook: "pr-job"})
	return r, ctrl, c
}

func ownerPing(text string) messageCreate {
	return messageCreate{
		ID: "m1", ChannelID: "c1", Content: text,
		Author:   dctl.Author{ID: "owner1", Username: "leo"},
		Mentions: []dctl.Author{{ID: "app1"}},
	}
}

func TestUnknownChannelAsksWhichRepo(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}, {Name: "Herrscherd/dctl"}}

	r.onMessage(context.Background(), ownerPing("fix the login bug"))

	if len(c.menus) != 1 || c.menus[0].customID != BindCustomID("c1") {
		t.Fatalf("menus = %+v, want one bind menu for c1", c.menus)
	}
	if len(ctrl.created) != 0 {
		t.Fatal("a session was created before the operator picked a repo")
	}
}

func TestPickCreatesBindsAndReplaysTheMessage(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	r.onMessage(context.Background(), ownerPing("fix the login bug"))

	r.onBindPick(context.Background(), "c1", "local:herrscher")

	if len(ctrl.created) != 1 {
		t.Fatalf("created = %+v, want one session", ctrl.created)
	}
	spec := ctrl.created[0]
	if spec.ChannelID != "c1" || spec.Project != "herrscher" || spec.Clone != "" {
		t.Fatalf("spec = %+v, want the channel adopted and a local project", spec)
	}
	got := ctrl.submitted[spec.Name]
	if len(got) != 1 || !strings.Contains(got[0].Text, "fix the login bug") {
		t.Fatalf("buffered message not replayed: %+v", got)
	}
	if !strings.Contains(got[0].Text, "pr-job") {
		t.Fatalf("playbook not named in the opening turn: %q", got[0].Text)
	}
	if r.binds.Session("c1") != spec.Name {
		t.Fatalf("binding = %q, want %q memorized for the channel", r.binds.Session("c1"), spec.Name)
	}
}

func TestRemotePickClonesInsteadOfProject(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "Herrscherd/dctl"}}
	r.onMessage(context.Background(), ownerPing("bug"))
	r.onBindPick(context.Background(), "c1", "remote:Herrscherd/dctl")

	if spec := ctrl.created[0]; spec.Clone != "Herrscherd/dctl" || spec.Project != "" {
		t.Fatalf("spec = %+v, want a clone", spec)
	}
}

func TestKnownChannelSubmitsDirectly(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.live["ch-c1"] = true
	if err := r.binds.Bind("c1", "ch-c1"); err != nil {
		t.Fatal(err)
	}
	r.onMessage(context.Background(), ownerPing("another one"))

	if len(c.menus) != 0 {
		t.Fatalf("a bound channel still asked: %+v", c.menus)
	}
	if got := ctrl.submitted["ch-c1"]; len(got) != 1 || !strings.Contains(got[0].Text, "another one") {
		t.Fatalf("submitted = %+v", got)
	}
}

func TestSubmitCarriesAttachmentsAndConversation(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	ctrl.live["ch-c1"] = true
	if err := r.binds.Bind("c1", "ch-c1"); err != nil {
		t.Fatal(err)
	}
	m := ownerPing("regarde le screen")
	m.Attachments = []dctl.Attachment{{Filename: "bug.png", URL: "https://cdn/bug.png", ContentType: "image/png", Size: 42}}
	r.onMessage(context.Background(), m)

	in := ctrl.submitted["ch-c1"][0]
	if in.Conversation.Gateway != "discord" || in.Conversation.ID != "c1" {
		t.Fatalf("conversation = %+v, want the pinged channel", in.Conversation)
	}
	if in.AuthorID != "owner1" || in.Author != "leo" || in.MessageID != "m1" {
		t.Fatalf("inbound identity = %+v", in)
	}
	if len(in.Attachments) != 1 || in.Attachments[0].URL != "https://cdn/bug.png" {
		t.Fatalf("attachments = %+v, want the screenshot handed to the host", in.Attachments)
	}
}

func TestNonTriggerMessagesAreIgnored(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	r.onMessage(context.Background(), messageCreate{
		ID: "m9", ChannelID: "c1", Content: "just chatting",
		Author: dctl.Author{ID: "someone-else"},
	})
	if len(c.menus) != 0 || len(ctrl.created) != 0 || len(ctrl.submitted) != 0 {
		t.Fatal("a non-trigger message reached the core")
	}
}

func TestStaleBindingIsDroppedAndReAsked(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	if err := r.binds.Bind("c1", "ch-gone"); err != nil { // never made live
		t.Fatal(err)
	}
	r.onMessage(context.Background(), ownerPing("hello?"))

	if r.binds.Session("c1") != "" {
		t.Fatal("stale binding survived a failed Submit")
	}
	if len(c.menus) != 1 {
		t.Fatalf("menus = %+v, want the repo question re-asked", c.menus)
	}
}

func TestSecondPingReplacesTheBufferedMessage(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	r.onMessage(context.Background(), ownerPing("first"))
	r.onMessage(context.Background(), ownerPing("second"))
	r.onBindPick(context.Background(), "c1", "local:herrscher")

	got := ctrl.submitted[ctrl.created[0].Name]
	if len(got) != 1 || !strings.Contains(got[0].Text, "second") || strings.Contains(got[0].Text, "first") {
		t.Fatalf("replayed %+v, want only the newest ping", got)
	}
}

func TestContextCarriesOtherPeoplesMessages(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.live["ch-c1"] = true
	if err := r.binds.Bind("c1", "ch-c1"); err != nil {
		t.Fatal(err)
	}
	c.read = []dctl.Message{
		{ID: "a", Content: "the button is broken", Author: dctl.Author{Username: "sam"}},
		{ID: "b", Content: "on mobile too", Author: dctl.Author{Username: "kim"}},
	}
	r.onMessage(context.Background(), ownerPing("fix that"))

	text := ctrl.submitted["ch-c1"][0].Text
	for _, want := range []string{"sam", "the button is broken", "kim", "fix that"} {
		if !strings.Contains(text, want) {
			t.Fatalf("context missing %q in:\n%s", want, text)
		}
	}
}

func TestNoReposTellsTheOperator(t *testing.T) {
	r, _, c := newTestRouter(t)
	r.onMessage(context.Background(), ownerPing("fix it"))
	if len(c.menus) != 0 {
		t.Fatalf("an empty repo list still posted a menu: %+v", c.menus)
	}
	if len(c.sent) != 1 || c.sent[0].channel != "c1" {
		t.Fatalf("sent = %+v, want one explanation in the pinged channel", c.sent)
	}
}

func TestPickOnADeadSessionReportsIt(t *testing.T) {
	r, _, _ := newTestRouter(t)
	if err := r.binds.Bind("c1", "ch-dead"); err != nil {
		t.Fatal(err)
	}
	if msg := r.onChoicePick(context.Background(), "ch-dead", "yes"); msg == "" {
		t.Fatal("a pick on a dead session must tell the operator, not fail silently")
	}
}
