package discord

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBindStoreRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	s := newBindStore(path, testLog())
	if got := s.Session("c1"); got != "" {
		t.Fatalf("empty store returned %q", got)
	}
	if err := s.Bind("c1", "ch-c1"); err != nil {
		t.Fatal(err)
	}
	if got := newBindStore(path, testLog()).Session("c1"); got != "ch-c1" {
		t.Fatalf("reloaded store returned %q, want ch-c1", got)
	}
	if err := s.Unbind("c1"); err != nil {
		t.Fatal(err)
	}
	if got := newBindStore(path, testLog()).Session("c1"); got != "" {
		t.Fatalf("unbound channel still resolves to %q", got)
	}
}

func TestBindStoreIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	if err := newBindStore(path, testLog()).Bind("c1", "s1"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestCorruptBindStoreStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := newBindStore(path, testLog()).Session("c1"); got != "" {
		t.Fatalf("corrupt store resolved %q, it must ask again, never guess a repo", got)
	}
}

func TestCorruptBindStoreKeepsNoThreads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	if err := os.WriteFile(path, []byte(`{"threads":{"c1":true},"channels":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if newBindStore(path, testLog()).IsThread("c1") {
		t.Fatal("a corrupt store granted a channel the no-@mention rule")
	}
}

func TestLevelRoundTripsAndResets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	s := newBindStore(path, testLog())
	if got := s.Level("c1"); got != "" {
		t.Fatalf("empty store returned level %q, want the configured default to win", got)
	}
	if err := s.SetLevel("c1", levelFull); err != nil {
		t.Fatal(err)
	}
	if got := newBindStore(path, testLog()).Level("c1"); got != levelFull {
		t.Fatalf("reloaded level = %q, want %q", got, levelFull)
	}
	if err := s.SetLevel("c1", ""); err != nil {
		t.Fatal(err)
	}
	if got := newBindStore(path, testLog()).Level("c1"); got != "" {
		t.Fatalf("reset level = %q, want the override gone", got)
	}
}

func TestUnbindKeepsTheRenderLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	s := newBindStore(path, testLog())
	if err := s.Bind("c1", "ch-c1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLevel("c1", levelQuiet); err != nil {
		t.Fatal(err)
	}
	if err := s.Unbind("c1"); err != nil {
		t.Fatal(err)
	}
	if got := newBindStore(path, testLog()).Level("c1"); got != levelQuiet {
		t.Fatalf("level = %q, want %q kept across the session's death", got, levelQuiet)
	}
}

func TestUnbindKeepsTheThreadFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	s := newBindStore(path, testLog())
	if err := s.BindThread("t1", "t1", "c1", "ch-t1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Unbind("t1"); err != nil {
		t.Fatal(err)
	}
	if got := newBindStore(path, testLog()); got.Session("t1") != "" || !got.IsThread("t1") {
		t.Fatalf("session = %q, thread = %v, want the binding gone and the thread kept",
			got.Session("t1"), got.IsThread("t1"))
	}
}

func TestAStoreFromBeforeModesReadsAndWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	if err := os.WriteFile(path, []byte(`{"channels":{"c1":"ch-c1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newBindStore(path, testLog())
	if got := s.Mode("c1"); got != "" {
		t.Fatalf("mode(c1) = %q, want the ordinary channel", got)
	}
	if err := s.SetMode("c1", supportMode); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepo("c1", "local:herrscher"); err != nil {
		t.Fatal(err)
	}
	if got := newBindStore(path, testLog()); got.Mode("c1") != supportMode || got.Repo("c1") != "local:herrscher" {
		t.Fatalf("mode = %q, repo = %q, want both persisted alongside the old binding",
			got.Mode("c1"), got.Repo("c1"))
	}
}

func TestBindStoreRefusesWritesOnCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newBindStore(path, testLog())
	if err := s.Bind("c1", "ch-c1"); !errors.Is(err, errStoreLocked) {
		t.Fatalf("a locked bind store must refuse changes with errStoreLocked, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{not json" {
		t.Fatalf("a corrupt file must be kept untouched, now %q (%v)", data, err)
	}
}

func TestBindStoreRefusesWritesOnUnreadableFile(t *testing.T) {
	s := newBindStore(t.TempDir(), testLog())
	if err := s.Bind("c1", "ch-c1"); !errors.Is(err, errStoreLocked) {
		t.Fatalf("an unreadable bind store must refuse changes, got %v", err)
	}
}
