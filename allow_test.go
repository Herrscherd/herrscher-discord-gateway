package discord

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestAllowGlobalBootstrap(t *testing.T) {
	s := newAllowStore(filepath.Join(t.TempDir(), "a.json"), testLog())
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
	s := newAllowStore(path, testLog())
	if err := s.AddGlobal("u1"); err != nil {
		t.Fatal(err)
	}
	if !newAllowStore(path, testLog()).Allowed("u1") {
		t.Fatal("allowlist did not persist across reload")
	}
}

func TestAllowSession(t *testing.T) {
	s := newAllowStore(filepath.Join(t.TempDir(), "a.json"), testLog())
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

func TestAllowRefusesEveryoneOnCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newAllowStore(path, testLog())
	if s.Allowed("anyone") {
		t.Fatal("a corrupt allow store must refuse access, not open it to everyone")
	}
	if err := s.AddGlobal("u1"); !errors.Is(err, errStoreLocked) {
		t.Fatalf("a locked store must refuse changes with errStoreLocked, got %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{not json" {
		t.Fatalf("a corrupt file must be kept untouched, now %q (%v)", data, err)
	}
}

func TestAllowRefusesEveryoneOnUnreadableFile(t *testing.T) {
	path := t.TempDir()
	if s := newAllowStore(path, testLog()); s.Allowed("anyone") {
		t.Fatal("an unreadable allow store must refuse access")
	}
}

func TestAllowWritesNeverOverlap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.json")
	s := newAllowStore(path, testLog())
	var inFlight atomic.Int32
	var overlapped atomic.Bool
	var mu sync.Mutex
	var last []byte
	s.write = func(_ string, data []byte) error {
		if inFlight.Add(1) > 1 {
			overlapped.Store(true)
		}
		runtime.Gosched()
		inFlight.Add(-1)
		mu.Lock()
		last = data
		mu.Unlock()
		return nil
	}
	const users = 50
	var wg sync.WaitGroup
	for i := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.AddGlobal(fmt.Sprintf("u%d", i)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if overlapped.Load() {
		t.Fatal("two snapshots were written at the same time")
	}
	mu.Lock()
	defer mu.Unlock()
	if got := len(s.ListGlobal()); got != users {
		t.Fatalf("in memory %d users, want %d", got, users)
	}
	if !containsAll(string(last), users) {
		t.Fatal("the last snapshot written is older than the in-memory state")
	}
}

func containsAll(snapshot string, users int) bool {
	for i := range users {
		if !strings.Contains(snapshot, fmt.Sprintf("\"u%d\"", i)) {
			return false
		}
	}
	return true
}
