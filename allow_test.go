package discord

import (
	"path/filepath"
	"testing"
)

func TestAllowGlobalBootstrap(t *testing.T) {
	s := newAllowStore(filepath.Join(t.TempDir(), "a.json"))
	// Empty global list allows everyone so the first operator can bootstrap.
	if !s.Allowed("anyone") {
		t.Fatal("empty allowlist should allow everyone")
	}
	if err := s.AddGlobal("u1"); err != nil {
		t.Fatal(err)
	}
	if s.Allowed("u2") {
		t.Fatal("non-empty allowlist should reject unlisted users")
	}
	if !s.Allowed("u1") {
		t.Fatal("listed user should be allowed")
	}
}

func TestAllowGlobalPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.json")
	s := newAllowStore(path)
	if err := s.AddGlobal("u1"); err != nil {
		t.Fatal(err)
	}
	// A fresh store over the same file sees the persisted entry.
	if !newAllowStore(path).Allowed("u1") {
		t.Fatal("allowlist did not persist across reload")
	}
}

func TestAllowSession(t *testing.T) {
	s := newAllowStore(filepath.Join(t.TempDir(), "a.json"))
	if err := s.AddSession("demo", "u1"); err != nil {
		t.Fatal(err)
	}
	if got := s.ListSession("demo"); len(got) != 1 || got[0] != "u1" {
		t.Fatalf("session list = %v", got)
	}
	if err := s.RemoveSession("demo", "u1"); err != nil {
		t.Fatal(err)
	}
	if got := s.ListSession("demo"); len(got) != 0 {
		t.Fatalf("session list after remove = %v", got)
	}
}
