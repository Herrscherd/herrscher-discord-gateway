package discord

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

func testRun() lifetime { return lifetimeFrom(context.Background()) }

func bufferLog(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

func TestStateDirWarnsWhenItCannotBeCreated(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DCTL_STATE_DIR", filepath.Join(blocker, "dctl"))

	var logs bytes.Buffer
	if _, err := stateDir(bufferLog(&logs)); err != nil {
		t.Fatalf("stateDir returned an error, want only a warning: %v", err)
	}
	if !strings.Contains(logs.String(), "create state directory failed") {
		t.Fatalf("no warning logged, got %q", logs.String())
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
