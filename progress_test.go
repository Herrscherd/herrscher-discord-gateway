package discord

import (
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

// Fifteen lines carrying 120 runes of detail each run past Discord's 2000-rune
// message limit. Discord rejects an oversized edit and flush swallows the error,
// so the live message would freeze for the rest of the turn.
func TestProgressBodyFitsDiscordsLimit(t *testing.T) {
	var last string
	pv := newProgressView(func(id, content string) (string, error) {
		last = content
		return "m1", nil
	}, levelFull, time.Unix(0, 0))

	for i := 0; i < maxLines*2; i++ {
		pv.add(contracts.BackendEvent{Kind: "tool", Tool: "Bash",
			Detail: strconv.Itoa(i) + strings.Repeat("x", 200)})
	}
	pv.flush(true)

	if n := utf8.RuneCountInString(last); n > gatewayMaxLen {
		t.Fatalf("progress body = %d runes, want <= %d", n, gatewayMaxLen)
	}
	if !strings.HasPrefix(last, "⏳ en cours…\n…\n") {
		t.Fatalf("progress body = %q…, want the dropped lines marked elided", last[:40])
	}
}

// quiet is the privacy level: the channel still sees that the agent is working,
// never what it worked on. A leaked absolute path is the whole reason it exists.
func TestQuietDropsToolDetailAndAssistantText(t *testing.T) {
	var last string
	pv := newProgressView(func(id, content string) (string, error) {
		last = content
		return "m1", nil
	}, "quiet", time.Unix(0, 0))

	pv.add(contracts.BackendEvent{Kind: "tool", Tool: "Read", Detail: "/home/op/secret-client/app.go"})
	pv.add(contracts.BackendEvent{Kind: "text", Detail: "looking at /home/op/secret-client"})
	pv.flush(true)

	if !strings.Contains(last, "📖 Read") {
		t.Fatalf("progress body = %q, want the tool line kept", last)
	}
	if strings.Contains(last, "/home/op") || strings.Contains(last, "app.go") {
		t.Fatalf("progress body = %q, want no path or filename", last)
	}
	if strings.Contains(last, "💭") {
		t.Fatalf("progress body = %q, want no assistant text at quiet", last)
	}
}

// Stripped of details, repeated tools all render the same line; left alone they
// would flush the 15-line window with copies of one another.
func TestRepeatedLinesCollapse(t *testing.T) {
	var last string
	pv := newProgressView(func(id, content string) (string, error) {
		last = content
		return "m1", nil
	}, "quiet", time.Unix(0, 0))

	for i := 0; i < 3; i++ {
		pv.add(contracts.BackendEvent{Kind: "tool", Tool: "Read", Detail: "a.go"})
	}
	pv.add(contracts.BackendEvent{Kind: "tool", Tool: "Bash", Detail: "go test"})
	pv.flush(true)

	if !strings.Contains(last, "📖 Read ×3") {
		t.Fatalf("progress body = %q, want the three Reads collapsed", last)
	}
	if n := strings.Count(last, "Read"); n != 1 {
		t.Fatalf("progress body = %q, want one Read line, got %d", last, n)
	}
	if !strings.Contains(last, "Bash") {
		t.Fatalf("progress body = %q, want the following tool kept on its own line", last)
	}
}

// full is unchanged: details are what make it useful, and it stays the default.
func TestFullKeepsDetail(t *testing.T) {
	var last string
	pv := newProgressView(func(id, content string) (string, error) {
		last = content
		return "m1", nil
	}, "full", time.Unix(0, 0))
	pv.add(contracts.BackendEvent{Kind: "tool", Tool: "Read", Detail: "/home/op/app.go"})
	pv.flush(true)
	if !strings.Contains(last, "/home/op/app.go") {
		t.Fatalf("progress body = %q, want the detail at full", last)
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
