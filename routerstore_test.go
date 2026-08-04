package discord

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBindStoreRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	s := newBindStore(path)
	if got := s.Session("c1"); got != "" {
		t.Fatalf("empty store returned %q", got)
	}
	if err := s.Bind("c1", "ch-c1"); err != nil {
		t.Fatal(err)
	}
	if got := newBindStore(path).Session("c1"); got != "ch-c1" {
		t.Fatalf("reloaded store returned %q, want ch-c1", got)
	}
	if err := s.Unbind("c1"); err != nil {
		t.Fatal(err)
	}
	if got := newBindStore(path).Session("c1"); got != "" {
		t.Fatalf("unbound channel still resolves to %q", got)
	}
}

func TestBindStoreIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	if err := newBindStore(path).Bind("c1", "s1"); err != nil {
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
	if got := newBindStore(path).Session("c1"); got != "" {
		t.Fatalf("corrupt store resolved %q — it must ask again, never guess a repo", got)
	}
}

// A half-decoded store must not leave a channel marked as one of our private
// threads: there a plain message from the owner makes the bot act, with no
// @mention to ask for it.
func TestCorruptBindStoreKeepsNoThreads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	if err := os.WriteFile(path, []byte(`{"threads":{"c1":true},"channels":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if newBindStore(path).IsThread("c1") {
		t.Fatal("a corrupt store granted a channel the no-@mention rule")
	}
}

// Unbinding drops the session, not the fact that the conversation is a private
// thread the gateway opened: the thread outlives the session running in it.
func TestUnbindKeepsTheThreadFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "router.json")
	s := newBindStore(path)
	if err := s.BindThread("t1", "ch-t1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Unbind("t1"); err != nil {
		t.Fatal(err)
	}
	if got := newBindStore(path); got.Session("t1") != "" || !got.IsThread("t1") {
		t.Fatalf("session = %q, thread = %v, want the binding gone and the thread kept",
			got.Session("t1"), got.IsThread("t1"))
	}
}
