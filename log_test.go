package discord

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func testLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

func testRun() lifetime { return lifetimeFrom(context.Background()) }

func bufferLog(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

func TestStateDirFailsWhenItCannotBeCreated(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DCTL_STATE_DIR", filepath.Join(blocker, "dctl"))

	if _, err := stateDir(); err == nil {
		t.Fatal("stateDir returned no error for an uncreatable directory, so the stores would open on a missing path")
	}
}

func TestUnconfiguredAccessWarnsOnce(t *testing.T) {
	var logs bytes.Buffer
	p := &accessPolicy{log: bufferLog(&logs)}
	p.warnUnconfigured()
	p.warnUnconfigured()
	if n := strings.Count(logs.String(), "nobody is authorized"); n != 1 {
		t.Fatalf("warning logged %d times, want once: %q", n, logs.String())
	}
}

func TestEmbeddedSkillsTreeShips(t *testing.T) {
	skillsTree, err := shipSkills(context.Background(), contracts.PluginConfig{})
	if err != nil {
		t.Fatalf("shipSkills: %v", err)
	}
	entries, err := fs.ReadDir(skillsTree, ".")
	if err != nil {
		t.Fatalf("read embedded skills: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("the embedded skills tree is empty")
	}
}
