package discord

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func modeSlash(t *testing.T, agent string) (*slash, *bindStore) {
	t.Helper()
	binds := newBindStore(filepath.Join(t.TempDir(), "router.json"))
	s := &slash{ctx: context.Background(), binds: binds}
	if agent != "" {
		s.router = newRouter(func() contracts.SessionControl { return nil }, &fakeClient{}, binds, nil,
			routerConfig{appID: "app1", agent: agent})
	}
	return s, binds
}

func TestAmbientModeIsRefusedWithoutAPersona(t *testing.T) {
	s, binds := modeSlash(t, "")

	got := s.setMode("c1", ambientMode)
	if !strings.Contains(got, "DISCORD_AGENT") {
		t.Fatalf("setMode = %q, want it refused and the missing persona named", got)
	}
	if mode := binds.Mode("c1"); mode != "" {
		t.Fatalf("mode(c1) = %q, want the channel left alone", mode)
	}
}

func TestAmbientModeIsAllowedWithAPersona(t *testing.T) {
	s, binds := modeSlash(t, "greeter")

	if got := s.setMode("c1", ambientMode); !strings.Contains(got, "ambient") {
		t.Fatalf("setMode = %q, want the mode confirmed", got)
	}
	if mode := binds.Mode("c1"); mode != ambientMode {
		t.Fatalf("mode(c1) = %q, want %q", mode, ambientMode)
	}
}

func TestOffModeIsStoredAndUnbinds(t *testing.T) {
	s, binds := modeSlash(t, "")
	if err := binds.Bind("c1", "ch-c1"); err != nil {
		t.Fatal(err)
	}

	if got := s.setMode("c1", offMode); !strings.Contains(got, "off") {
		t.Fatalf("setMode = %q, want the mode confirmed", got)
	}
	if mode := binds.Mode("c1"); mode != offMode {
		t.Fatalf("mode(c1) = %q, want %q", mode, offMode)
	}
	if got := binds.Session("c1"); got != "" {
		t.Fatalf("session(c1) = %q, want the old binding dropped", got)
	}
}

func TestEveryModeTheCommandOffersIsKnown(t *testing.T) {
	for _, mode := range []string{normalMode, supportMode, ambientMode, offMode} {
		if !knownMode(mode) {
			t.Fatalf("%q is offered but not recognised", mode)
		}
	}
	if knownMode("helpdesk") {
		t.Fatal("an unknown mode was recognised")
	}
}

func TestAThreadInheritsItsChannelsMode(t *testing.T) {
	r, _, _ := newGuildRouter(t, nil)
	if err := r.binds.SetMode("c1", ambientMode); err != nil {
		t.Fatal(err)
	}
	if err := r.binds.BindThread("t1", "t1", "c1", "ch-t1"); err != nil {
		t.Fatal(err)
	}

	if got := r.mode("t1", "c1"); got != ambientMode {
		t.Fatalf("mode(t1) = %q, want the parent's %q", got, ambientMode)
	}
	if got := r.mode("c9", ""); got != normalMode {
		t.Fatalf("mode(c9) = %q, want %q for a room nobody configured", got, normalMode)
	}
}
