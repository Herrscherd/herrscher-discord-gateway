package discord

import (
	"strings"
	"testing"
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestProgressViewRendersToolLineWithEmoji(t *testing.T) {
	var last string
	pv := newProgressView(func(id, content string) (string, error) {
		last = content
		return "m1", nil
	}, "full", time.Unix(0, 0))

	pv.add(contracts.BackendEvent{Kind: "tool", Tool: "Read", Detail: "envfile.go"})
	pv.flush(true)

	if !strings.Contains(last, "📖 Read") || !strings.Contains(last, "envfile.go") {
		t.Fatalf("progress body = %q, want emoji+tool+detail", last)
	}
}

func TestProgressViewSummaryCountsActions(t *testing.T) {
	posted := []string{}
	pv := newProgressView(func(id, content string) (string, error) {
		posted = append(posted, content)
		return "m1", nil
	}, "full", time.Unix(0, 0))
	pv.add(contracts.BackendEvent{Kind: "tool", Tool: "Read"})
	pv.add(contracts.BackendEvent{Kind: "tool", Tool: "Read"})
	pv.add(contracts.BackendEvent{Kind: "result", Cost: 0.02})
	pv.finish()

	got := posted[len(posted)-1]
	if !strings.HasPrefix(got, "✅") || !strings.Contains(got, "2 actions") || !strings.Contains(got, "Read×2") {
		t.Fatalf("summary = %q, want ✅ 2 actions (Read×2)", got)
	}
}
