package discord

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

// fakeCtrl records what the router asked the core to do.
type fakeCtrl struct {
	repos     []contracts.RepoRef
	sessions  []contracts.SessionInfo
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
func (f *fakeCtrl) Sessions() []contracts.SessionInfo                   { return f.sessions }
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
	r, ctrl, c, _ := newTestRouterRendering(t)
	return r, ctrl, c
}

// newTestRouterRendering also hands back the render fake, for the tests that
// assert on what the operator sees rather than on what the core is told.
func newTestRouterRendering(t *testing.T) (*router, *fakeCtrl, *fakeClient, *fakeRender) {
	t.Helper()
	ctrl, c, f := newFakeCtrl(), &fakeClient{}, &fakeRender{}
	binds := newBindStore(filepath.Join(t.TempDir(), "router.json"))
	r := newRouter(func() contracts.SessionControl { return ctrl }, c, binds,
		newSinks(context.Background(), f, "full", ""),
		routerConfig{owner: "owner1", appID: "app1", contextMessages: 5, playbook: "pr-job"})
	return r, ctrl, c, f
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

// The repo question is the one reply that is not a turn, so nothing else would
// mark the ping as received while the operator reads the menu.
func TestAskAcksThePingItIsAnswering(t *testing.T) {
	r, ctrl, _, f := newTestRouterRendering(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}

	r.onMessage(context.Background(), ownerPing("fix the login bug"))

	if len(f.reacted) != 1 || f.reacted[0] != ackEmoji {
		t.Fatalf("reacted = %v, want one %q on the ping that asked the question", f.reacted, ackEmoji)
	}
	// The turn that follows the pick reuses that same ⏳ rather than adding one,
	// and clears it exactly once when the turn ends.
	r.onBindPick(context.Background(), "c1", "local:herrscher")
	s := r.sinks.at("c1")
	s.handle(contracts.Event{T: "human"})
	if len(f.reacted) != 1 {
		t.Fatalf("reacted = %v, want the ack not to be repeated by the turn", f.reacted)
	}
	s.handle(contracts.Event{T: "reply", Text: "done", Done: true})
	if len(f.unreacted) != 1 || f.unreacted[0] != ackEmoji {
		t.Fatalf("unreacted = %v, want the ack cleared once at turn end", f.unreacted)
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

// The rule that no work survives a turn has to be on every turn: the opening
// one is the least likely place for the agent to promise background work.
func TestEveryTurnCarriesTheNoBackgroundWorkRule(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	ctrl.live["ch-c1"] = true
	if err := r.binds.Bind("c1", "ch-c1"); err != nil {
		t.Fatal(err)
	}
	r.onMessage(context.Background(), ownerPing("first"))
	r.onMessage(context.Background(), ownerPing("second"))

	for i, in := range ctrl.submitted["ch-c1"] {
		if !strings.Contains(in.Text, "rien ne continue en tâche de fond") {
			t.Fatalf("turn %d dropped the rule:\n%s", i, in.Text)
		}
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

// A choice menu carries the conversation it was posted into, not a session name
// (Gateway.Menu only ever knows the conversation), so the pick must be resolved
// back to the session driving that channel — bound or created by command.
func TestChoicePickResolvesTheChannelToItsSession(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	ctrl.live["ch-c1"] = true
	if err := r.binds.Bind("c1", "ch-c1"); err != nil {
		t.Fatal(err)
	}
	if msg := r.onChoicePick(context.Background(), "c1", "yes"); msg != "" {
		t.Fatalf("pick on a bound channel = %q, want it to land", msg)
	}
	if got := ctrl.picked["ch-c1"]; len(got) != 1 || got[0] != "yes" {
		t.Fatalf("picked = %v, want the bound session to receive it", ctrl.picked)
	}

	// A session created by `/session create` has no binding; the live session list
	// is what maps its channel back to its name.
	ctrl.live["demo"] = true
	ctrl.sessions = []contracts.SessionInfo{{Name: "demo", ChannelID: "c9"}}
	if msg := r.onChoicePick(context.Background(), "c9", "no"); msg != "" {
		t.Fatalf("pick on a command-created session = %q, want it to land", msg)
	}
	if got := ctrl.picked["demo"]; len(got) != 1 || got[0] != "no" {
		t.Fatalf("picked = %v, want demo to receive it", ctrl.picked)
	}
}

// The repo is usually named in the ping itself; the menu is friction there.
func TestNamedRepoSkipsTheMenu(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}, {Name: "Herrscherd/dctl"}}

	r.onMessage(context.Background(), ownerPing("le bug d'auth de herrscher, tu regardes ?"))

	if len(c.menus) != 0 {
		t.Fatalf("menus = %+v, want none — the ping already said which repo", c.menus)
	}
	if len(ctrl.created) != 1 || ctrl.created[0].Project != "herrscher" {
		t.Fatalf("created = %+v, want a session on herrscher", ctrl.created)
	}
	got := ctrl.submitted[ctrl.created[0].Name]
	if len(got) != 1 || !strings.Contains(got[0].Text, "le bug d'auth") {
		t.Fatalf("submitted = %+v, want the ping run straight away", got)
	}
	if r.binds.Session("c1") != ctrl.created[0].Name {
		t.Fatal("the channel was not bound to the session it created")
	}
}

// Two repos named is not an answer: binding the wrong one outlives the message.
func TestAmbiguousRepoStillAsks(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}, {Name: "Herrscherd/dctl"}}

	r.onMessage(context.Background(), ownerPing("aligne herrscher et dctl"))

	if len(c.menus) != 1 {
		t.Fatalf("menus = %+v, want the question asked", c.menus)
	}
	if len(ctrl.created) != 0 {
		t.Fatalf("created = %+v, want nothing before the operator picks", ctrl.created)
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
	// A thread nobody was added to is a room only the bot can read.
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
	// The ⏳ still belongs on the ping, which lives in the parent channel.
	if got := r.sinks.at("t1").lastUser; got != (msgRef{ch: "c1", id: "m1"}) {
		t.Fatalf("lastUser = %+v, want the ping in its own channel", got)
	}
	if len(f.reacted) != 1 || f.reacted[0] != ackEmoji {
		t.Fatalf("reacted = %v, want the ping acked", f.reacted)
	}
}

// Inside a thread the gateway opened, a bare message is for the bot: nobody
// else is in the room.
func TestThreadMessagesNeedNoMention(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	ctrl.live["ch-t1"] = true
	if err := r.binds.BindThread("t1", "ch-t1"); err != nil {
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

// Without the privileged MESSAGE_CONTENT intent, Discord blanks the body of
// every message that does not @mention the bot — which is exactly the bare
// messages a private thread exists to accept. The text is read back over REST,
// which the intent does not gate; otherwise the turn would carry no instruction.
func TestBlankedThreadMessageIsReadBack(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.live["ch-t1"] = true
	if err := r.binds.BindThread("t1", "ch-t1"); err != nil {
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

// Work asked for in private must never quietly land in a room other people
// read: the fallback happens, but it is announced.
func TestThreadFailureFallsBackToTheChannelOutLoud(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	c.threadErr = errors.New("missing permission")

	r.onMessage(context.Background(), ownerPing("en privé stp, herrscher"))

	if len(c.sent) != 1 || c.sent[0].channel != "c1" || !strings.Contains(c.sent[0].content, "fil privé") {
		t.Fatalf("sent = %+v, want the fallback said out loud in the channel", c.sent)
	}
	if spec := ctrl.created[0]; spec.ChannelID != "c1" {
		t.Fatalf("spec = %+v, want the job to fall back to the channel", spec)
	}
}

// A thread the operator cannot see is worse than no thread at all.
func TestThreadWithoutItsMemberIsNotUsed(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	c.memberErr = errors.New("forbidden")

	r.onMessage(context.Background(), ownerPing("dans un fil, herrscher"))

	if spec := ctrl.created[0]; spec.ChannelID != "c1" {
		t.Fatalf("spec = %+v, want the channel rather than a thread only the bot can read", spec)
	}
}

// A daemon restart kills the session but not the thread. Rebinding must keep it
// a thread of ours, or the operator would suddenly have to @mention the bot in
// a room that holds nobody else — and a second thread would be opened inside it.
func TestRebindingInsideAThreadStaysInIt(t *testing.T) {
	r, ctrl, c := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	if err := r.binds.BindThread("t1", "ch-t1"); err != nil {
		t.Fatal(err)
	}
	// The session is gone: Submit fails, the binding is dropped, the router asks
	// again — here answered by the repo named in the message.
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

func TestPickOnADeadSessionReportsIt(t *testing.T) {
	r, _, _ := newTestRouter(t)
	if err := r.binds.Bind("c1", "ch-dead"); err != nil {
		t.Fatal(err)
	}
	if msg := r.onChoicePick(context.Background(), "ch-dead", "yes"); msg == "" {
		t.Fatal("a pick on a dead session must tell the operator, not fail silently")
	}
}
