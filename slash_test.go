package discord

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestRouteSubcommand(t *testing.T) {
	d := dctl.InteractionData{
		Name: "set",
		Options: []dctl.InteractionOption{{
			Name: "home", Type: 1,
			Options: []dctl.InteractionOption{{Name: "channel", Type: 7, Value: "cat1"}},
		}},
	}
	path, leaves := route(d)
	if !slices.Equal(path, []string{"set", "home"}) {
		t.Fatalf("path = %v", path)
	}
	if len(leaves) != 1 || leaves[0].Name != "channel" {
		t.Fatalf("leaves = %+v", leaves)
	}
}

func TestRouteGroup(t *testing.T) {
	d := dctl.InteractionData{
		Name: "session",
		Options: []dctl.InteractionOption{{
			Name: "allow", Type: 2,
			Options: []dctl.InteractionOption{{
				Name: "add", Type: 1,
				Options: []dctl.InteractionOption{
					{Name: "name", Type: 3, Value: "demo"},
					{Name: "user", Type: 6, Value: "u1"},
				},
			}},
		}},
	}
	path, leaves := route(d)
	if !slices.Equal(path, []string{"session", "allow", "add"}) {
		t.Fatalf("path = %v", path)
	}
	if len(leaves) != 2 {
		t.Fatalf("leaves = %+v", leaves)
	}
}

func TestFlagsFrom(t *testing.T) {
	leaves := []dctl.InteractionOption{
		{Name: "name", Type: 3, Value: "demo"},
		{Name: "shared", Type: 5, Value: true},
		{Name: "force", Type: 5, Value: false},
		{Name: "backend", Type: 3, Value: "oneshot"},
	}
	got := flagsFrom(leaves)
	want := []string{"--name", "demo", "--shared", "--backend", "oneshot"}
	if !slices.Equal(got, want) {
		t.Fatalf("flagsFrom = %v, want %v", got, want)
	}
}

func TestValStr(t *testing.T) {
	if valStr("x") != "x" || valStr(true) != "true" || valStr(float64(42)) != "42" {
		t.Fatalf("valStr mismatch")
	}
}

func TestListUsers(t *testing.T) {
	if got := listUsers("allowlist", nil); got != "allowlist: empty (everyone allowed)" {
		t.Fatalf("empty list = %q", got)
	}
	if got := listUsers("allowlist", []string{"a", "b"}); got != "allowlist: <@a>, <@b>" {
		t.Fatalf("list = %q", got)
	}
}

func testAllowStore(t *testing.T) *allowStore {
	t.Helper()
	return newAllowStore(filepath.Join(t.TempDir(), "allow.json"), testLog())
}

type fakeAcker struct{ acked []string }

func (f *fakeAcker) Ack(_ context.Context, _, _, content string) error {
	f.acked = append(f.acked, content)
	return nil
}

func TestComponentInteractionRoutesToPickNotTheRegistry(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	ctrl.live["ch-c1"] = true
	ack := &fakeAcker{}
	s := &slash{log: testLog(), router: r, comp: ack, allow: testAllowStore(t)}

	s.onInteraction(context.Background(), dctl.Interaction{
		Type:      dctl.InteractionComponent,
		ChannelID: "c1",
		Data:      dctl.InteractionData{CustomID: ChoiceCustomID("ch-c1"), Values: []string{"yes"}},
	})

	if got := ctrl.picked["ch-c1"]; len(got) != 1 || got[0] != "yes" {
		t.Fatalf("picked = %v, want [yes]", got)
	}
	if len(ack.acked) != 1 {
		t.Fatalf("acked = %v, want the click acknowledged so the dropdown collapses", ack.acked)
	}
}

func TestBindComponentInteractionCreatesTheSession(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	r.onMessage(context.Background(), ownerPing("fix it"))
	s := &slash{log: testLog(), router: r, comp: &fakeAcker{}, allow: testAllowStore(t)}

	s.onInteraction(context.Background(), dctl.Interaction{
		Type:      dctl.InteractionComponent,
		ChannelID: "c1",
		Data:      dctl.InteractionData{CustomID: BindCustomID("c1"), Values: []string{"local:herrscher"}},
	})

	if len(ctrl.created) != 1 {
		t.Fatalf("created = %+v, want the session created from the click", ctrl.created)
	}
}

func TestUnknownComponentIsDropped(t *testing.T) {
	r, _, _ := newTestRouter(t)
	ack := &fakeAcker{}
	s := &slash{log: testLog(), router: r, comp: ack, allow: testAllowStore(t)}
	s.onInteraction(context.Background(), dctl.Interaction{
		Type: dctl.InteractionComponent,
		Data: dctl.InteractionData{CustomID: "someone-elses-menu", Values: []string{"x"}},
	})
	if len(ack.acked) != 0 {
		t.Fatalf("acked = %v, want nothing", ack.acked)
	}
}

func TestAllowGateReadsTheCallerInDMsToo(t *testing.T) {
	allow := newAllowStore(filepath.Join(t.TempDir(), "allow.json"), testLog())
	if err := allow.AddGlobal("owner1"); err != nil {
		t.Fatal(err)
	}
	s := &slash{log: testLog(), allow: allow}
	if !s.gate(context.Background(), dctl.Interaction{User: dctl.Author{ID: "owner1"}}) {
		t.Fatal("the owner was denied in a DM")
	}
	if !s.gate(context.Background(), dctl.Interaction{Member: dctl.Member{User: dctl.Author{ID: "owner1"}}}) {
		t.Fatal("the owner was denied in a guild")
	}
}

func TestStopInterruptsTheSessionThisConversationIsBoundTo(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	if err := r.binds.Bind("c1", "ch-c1"); err != nil {
		t.Fatal(err)
	}
	ctrl.live["ch-c1"] = true
	s := &slash{log: testLog(), router: r, ctrl: ctrl}

	if got := s.stop("c1", "", true); !strings.Contains(got, "interrompu") {
		t.Fatalf("stop = %q, want the turn reported as interrupted", got)
	}
	if !slices.Equal(ctrl.interrupted, []string{"ch-c1"}) {
		t.Fatalf("interrupted = %v, want [ch-c1]", ctrl.interrupted)
	}
}

func TestStopReportsWhenNothingIsRunning(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	s := &slash{log: testLog(), router: r, ctrl: ctrl}
	if got := s.stop("c1", "", true); !strings.Contains(got, "aucun tour") {
		t.Fatalf("stop = %q, want it to report no live turn", got)
	}
}

func TestVerbosityRetunesOneConversationOnly(t *testing.T) {
	binds := newBindStore(filepath.Join(t.TempDir(), "router.json"), testLog())
	s := &slash{log: testLog(), binds: binds}

	if got := s.setVerbosity("c1", levelFull); !strings.Contains(got, levelFull) {
		t.Fatalf("setVerbosity = %q, want the new level confirmed", got)
	}
	if got := binds.Level("c1"); got != levelFull {
		t.Fatalf("level(c1) = %q, want %q", got, levelFull)
	}
	if got := binds.Level("c2"); got != "" {
		t.Fatalf("level(c2) = %q, retuning one room changed another", got)
	}
}

func TestVerbosityRefusesAnUnknownLevel(t *testing.T) {
	binds := newBindStore(filepath.Join(t.TempDir(), "router.json"), testLog())
	s := &slash{log: testLog(), binds: binds}

	if got := s.setVerbosity("c1", "loud"); !strings.Contains(got, "inconnu") {
		t.Fatalf("setVerbosity = %q, want it refused", got)
	}
	if got := binds.Level("c1"); got != "" {
		t.Fatalf("level(c1) = %q, want nothing stored", got)
	}
}

func TestReadIsSuppressedForPushDrivenChannels(t *testing.T) {
	binds := newBindStore(filepath.Join(t.TempDir(), "router.json"), testLog())
	if err := binds.Bind("c1", "ch-c1"); err != nil {
		t.Fatal(err)
	}
	p := &Platform{binds: binds}
	p.readImpl = func(context.Context, string, int, string) ([]rawMsg, error) {
		t.Fatal("Read hit the network for a push-driven channel")
		return nil, nil
	}
	got, err := p.Read(context.Background(), "c1", 100, "")
	if err != nil || len(got) != 0 {
		t.Fatalf("Read = %v, %v; want empty and no error", got, err)
	}
}

func TestSupportModeUnbindsTheChannel(t *testing.T) {
	binds := newBindStore(filepath.Join(t.TempDir(), "router.json"), testLog())
	s := &slash{log: testLog(), binds: binds}
	if err := binds.Bind("c1", "ch-c1"); err != nil {
		t.Fatal(err)
	}

	if got := s.setMode("c1", supportMode); !strings.Contains(got, "support") {
		t.Fatalf("setMode = %q, want the mode confirmed", got)
	}
	if got := binds.Mode("c1"); got != supportMode {
		t.Fatalf("mode(c1) = %q, want %q", got, supportMode)
	}
	if got := binds.Session("c1"); got != "" {
		t.Fatalf("session(c1) = %q, want the old binding dropped", got)
	}
	if got := binds.Mode("c2"); got != "" {
		t.Fatalf("mode(c2) = %q, one room's mode changed another", got)
	}
}

func TestNormalModeTakesTheChannelBackOut(t *testing.T) {
	binds := newBindStore(filepath.Join(t.TempDir(), "router.json"), testLog())
	s := &slash{log: testLog(), binds: binds}
	if got := s.setMode("c1", supportMode); got == "" {
		t.Fatal("setMode said nothing")
	}

	if got := s.setMode("c1", normalMode); !strings.Contains(got, "normal") {
		t.Fatalf("setMode = %q, want the mode confirmed", got)
	}
	if got := binds.Mode("c1"); got != "" {
		t.Fatalf("mode(c1) = %q, want nothing stored", got)
	}
}

func TestAnUnknownModeIsRefused(t *testing.T) {
	binds := newBindStore(filepath.Join(t.TempDir(), "router.json"), testLog())
	s := &slash{log: testLog(), binds: binds}

	if got := s.setMode("c1", "helpdesk"); !strings.Contains(got, "inconnu") {
		t.Fatalf("setMode = %q, want it refused", got)
	}
	if got := binds.Mode("c1"); got != "" {
		t.Fatalf("mode(c1) = %q, want nothing stored", got)
	}
}

func TestASyncThatIsRateLimitedIsRetried(t *testing.T) {
	backoff := []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}

	calls := 0
	syncWithRetry(context.Background(), func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("429: You are being rate limited")
		}
		return nil
	}, testLog(), backoff)

	if calls != 3 {
		t.Fatalf("calls = %d, want the sync retried until it landed", calls)
	}
}

func TestASyncThatKeepsFailingGivesUp(t *testing.T) {
	backoff := []time.Duration{time.Millisecond}

	calls := 0
	syncWithRetry(context.Background(), func(context.Context) error {
		calls++
		return errors.New("nope")
	}, testLog(), backoff)

	if calls != len(backoff)+1 {
		t.Fatalf("calls = %d, want %d attempts then a report", calls, len(backoff)+1)
	}
}

func TestASyncStopsWhenTheDaemonDoes(t *testing.T) {
	backoff := []time.Duration{time.Hour}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		syncWithRetry(ctx, func(context.Context) error {
			cancel()
			return errors.New("429")
		}, testLog(), backoff)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the sync outlived the daemon it belongs to")
	}
}

func TestComponentClicksAreGatedByTheAllowList(t *testing.T) {
	cases := []struct {
		name    string
		allowed string
		caller  string
		want    bool
	}{
		{"allowed caller answers the menu", "u1", "u1", true},
		{"stranger is refused", "u1", "u2", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, ctrl, _ := newTestRouter(t)
			ctrl.live["ch-c1"] = true
			allow := testAllowStore(t)
			if err := allow.AddGlobal(tc.allowed); err != nil {
				t.Fatal(err)
			}
			ack := &fakeAcker{}
			s := &slash{log: testLog(), router: r, comp: ack, allow: allow}

			s.onInteraction(context.Background(), dctl.Interaction{
				Type:      dctl.InteractionComponent,
				ChannelID: "c1",
				Member:    dctl.Member{User: dctl.Author{ID: tc.caller}},
				Data:      dctl.InteractionData{CustomID: ChoiceCustomID("ch-c1"), Values: []string{"yes"}},
			})

			if got := len(ctrl.picked["ch-c1"]) == 1; got != tc.want {
				t.Fatalf("picked = %v, want answered = %v", ctrl.picked, tc.want)
			}
			if len(ack.acked) != 1 {
				t.Fatalf("acked = %v, want the click acknowledged", ack.acked)
			}
		})
	}
}
