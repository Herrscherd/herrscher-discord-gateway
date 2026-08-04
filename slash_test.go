package discord

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

// route walks groups + sub-commands and returns the command path plus the leaf
// options of the deepest sub. This is the crux of keeping the core neutral: the
// path becomes the argv the SessionControl seam dispatches.
func TestRouteSubcommand(t *testing.T) {
	d := dctl.InteractionData{
		Name: "set",
		Options: []dctl.InteractionOption{{
			Name: "home", Type: 1,
			Options: []dctl.InteractionOption{{Name: "channel", Type: 7, Value: "cat1"}},
		}},
	}
	path, leaves := route(d)
	if !reflect.DeepEqual(path, []string{"set", "home"}) {
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
	if !reflect.DeepEqual(path, []string{"session", "allow", "add"}) {
		t.Fatalf("path = %v", path)
	}
	if len(leaves) != 2 {
		t.Fatalf("leaves = %+v", leaves)
	}
}

// flagsFrom turns leaf options into the `--name value` / bare `--flag` argv the
// core CLI registry parses; a false bool emits nothing.
func TestFlagsFrom(t *testing.T) {
	leaves := []dctl.InteractionOption{
		{Name: "name", Type: 3, Value: "demo"},
		{Name: "shared", Type: 5, Value: true},
		{Name: "force", Type: 5, Value: false},
		{Name: "backend", Type: 3, Value: "oneshot"},
	}
	got := flagsFrom(leaves)
	want := []string{"--name", "demo", "--shared", "--backend", "oneshot"}
	if !reflect.DeepEqual(got, want) {
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

// fakeAcker records the acknowledgement a component click is answered with.
type fakeAcker struct{ acked []string }

func (f *fakeAcker) Ack(_ context.Context, _, _, content string) error {
	f.acked = append(f.acked, content)
	return nil
}

func TestComponentInteractionRoutesToPickNotTheRegistry(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	ctrl.live["ch-c1"] = true
	ack := &fakeAcker{}
	s := &slash{ctx: context.Background(), router: r, comp: ack}

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
	s := &slash{ctx: context.Background(), router: r, comp: &fakeAcker{}}

	s.onInteraction(context.Background(), dctl.Interaction{
		Type:      dctl.InteractionComponent,
		ChannelID: "c1",
		Data:      dctl.InteractionData{CustomID: BindCustomID("c1"), Values: []string{"local:herrscher"}},
	})

	if len(ctrl.created) != 1 {
		t.Fatalf("created = %+v, want the session created from the click", ctrl.created)
	}
}

// An unknown custom_id belongs to nobody: it must not reach the command registry
// (which has no handler for a component) nor be acknowledged as if it worked.
func TestUnknownComponentIsDropped(t *testing.T) {
	r, _, _ := newTestRouter(t)
	ack := &fakeAcker{}
	s := &slash{ctx: context.Background(), router: r, comp: ack}
	s.onInteraction(context.Background(), dctl.Interaction{
		Type: dctl.InteractionComponent,
		Data: dctl.InteractionData{CustomID: "someone-elses-menu", Values: []string{"x"}},
	})
	if len(ack.acked) != 0 {
		t.Fatalf("acked = %v, want nothing", ack.acked)
	}
}

func TestReadIsSuppressedForPushDrivenChannels(t *testing.T) {
	binds := newBindStore(filepath.Join(t.TempDir(), "router.json"))
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
