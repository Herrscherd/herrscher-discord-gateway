package discord

import (
	"context"
	"slices"
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func turnWithReply(outcome contracts.TurnOutcome, text string) *fakeRender {
	f := &fakeRender{channel: "c1"}
	s := newTestSink(f)
	s.noteUser(context.Background(), "c1", "u1", "42")
	s.handle(contracts.Event{T: "human", Who: "alice", Text: "hi"})
	s.handle(contracts.Event{T: "status", Text: "Read envfile.go"})
	s.handle(contracts.Event{T: "reply", Text: text, Done: true, Outcome: outcome})
	return f
}

func TestTurnWithoutOutcomeEndsAsASuccess(t *testing.T) {
	f := turnWithReply("", "done")

	if want := []string{"done"}; !slices.Equal(f.posts, want) {
		t.Fatalf("posts = %v, want %v", f.posts, want)
	}
	if want := []string{englishHead(headAck)}; !slices.Equal(f.reacted, want) {
		t.Fatalf("reacted = %v, want only the ack %v", f.reacted, want)
	}
	if want := []string{englishHead(headAck)}; !slices.Equal(f.unreacted, want) {
		t.Fatalf("unreacted = %v, want the ack cleared %v", f.unreacted, want)
	}
	if last := f.upserts[len(f.upserts)-1]; !strings.HasPrefix(last, englishHead(headDone)+" ") {
		t.Fatalf("final status = %q, want the done summary", last)
	}
}

func TestFailedTurnEndsWithAFailureReaction(t *testing.T) {
	f := turnWithReply(contracts.TurnFailed, "the backend failed")

	if want := []string{englishHead(headFail) + " the backend failed"}; !slices.Equal(f.posts, want) {
		t.Fatalf("posts = %v, want %v", f.posts, want)
	}
	if want := []string{englishHead(headAck), englishHead(headFail)}; !slices.Equal(f.reacted, want) {
		t.Fatalf("reacted = %v, want %v", f.reacted, want)
	}
	if want := []string{englishHead(headAck)}; !slices.Equal(f.unreacted, want) {
		t.Fatalf("unreacted = %v, want the ack cleared %v", f.unreacted, want)
	}
	if last := f.upserts[len(f.upserts)-1]; !strings.HasPrefix(last, englishHead(headFail)+" ") {
		t.Fatalf("final status = %q, want the failure head on the summary", last)
	}
}

func TestInterruptedTurnEndsWithAStopReactionAndNoReply(t *testing.T) {
	f := turnWithReply(contracts.TurnInterrupted, "")

	if len(f.posts) != 0 {
		t.Fatalf("posts = %v, want none: the stop confirmation is already the reply", f.posts)
	}
	if want := []string{englishHead(headAck), englishHead(headStop)}; !slices.Equal(f.reacted, want) {
		t.Fatalf("reacted = %v, want %v", f.reacted, want)
	}
	if want := []string{englishHead(headAck)}; !slices.Equal(f.unreacted, want) {
		t.Fatalf("unreacted = %v, want the ack cleared %v", f.unreacted, want)
	}
	if last := f.upserts[len(f.upserts)-1]; !strings.HasPrefix(last, englishHead(headStop)+" ") {
		t.Fatalf("final status = %q, want the stop head on the summary", last)
	}
}

func toolDoneTurn(state contracts.ToolCallState) string {
	f := &fakeRender{channel: "c1"}
	s := newTestSink(f)
	s.handle(contracts.Event{T: "human"})
	s.handle(contracts.Event{T: "status", Text: "Bash ls", ToolCall: &contracts.ToolCall{ID: "t1", Tool: "Bash", State: contracts.ToolCallStarted}})
	s.handle(contracts.Event{T: "tool_done", ToolCall: &contracts.ToolCall{ID: "t1", Tool: "Bash", State: state}})
	s.handle(contracts.Event{T: "abandoned"})
	return f.upserts[len(f.upserts)-1]
}

func TestFailedToolCallIsShownAsALine(t *testing.T) {
	last := toolDoneTurn(contracts.ToolCallFailed)

	if !strings.Contains(last, englishHead(headFail)+" Bash") {
		t.Fatalf("status = %q, want a failed line for Bash", last)
	}
}

func TestSucceededToolCallAddsNoLine(t *testing.T) {
	last := toolDoneTurn(contracts.ToolCallSucceeded)

	if strings.Contains(last, englishHead(headFail)) {
		t.Fatalf("status = %q, want no failed line for a call that succeeded", last)
	}
	if n := strings.Count(last, "Bash"); n != 1 {
		t.Fatalf("status = %q, want the call once (its start line), got %d", last, n)
	}
}

func tidyTurn(outcome contracts.TurnOutcome, text string) *fakeRender {
	f := &fakeRender{channel: "c1"}
	s := newSink(testRun(), f, "c1", staticLevel(levelSilent), "42", true, testMessages(), testLog())
	s.noteUser(context.Background(), "c1", "u1", "42")
	s.handle(contracts.Event{T: "human"})
	s.handle(contracts.Event{T: "reply", Text: text, Done: true, Outcome: outcome})
	return f
}

func TestFailedAndInterruptedTurnsKeepTheOpeningPing(t *testing.T) {
	cases := []struct {
		name     string
		outcome  contracts.TurnOutcome
		text     string
		reaction headKey
	}{
		{name: "failed", outcome: contracts.TurnFailed, text: "the backend failed", reaction: headFail},
		{name: "interrupted", outcome: contracts.TurnInterrupted, text: "", reaction: headStop},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tidyTurn(tc.outcome, tc.text)

			if len(f.deleted) != 0 {
				t.Fatalf("deleted = %v, want the opening ping kept", f.deleted)
			}
			if want := []string{englishHead(headAck), englishHead(tc.reaction)}; !slices.Equal(f.reacted, want) {
				t.Fatalf("reacted = %v, want %v", f.reacted, want)
			}
		})
	}
}

func TestSucceededTurnDeletesTheOpeningPing(t *testing.T) {
	f := tidyTurn("", "done")

	if want := []msgRef{{ch: "c1", id: "u1"}}; !slices.Equal(f.deleted, want) {
		t.Fatalf("deleted = %v, want the opening ping %v", f.deleted, want)
	}
	if want := []string{englishHead(headAck)}; !slices.Equal(f.reacted, want) {
		t.Fatalf("reacted = %v, want only the ack %v", f.reacted, want)
	}
}

func TestFailedToolCallErrorTextNeverReachesDiscord(t *testing.T) {
	const secret = "token sk-fixture-7f3a9c"
	f := &fakeRender{channel: "c1"}
	s := newTestSink(f)
	s.handle(contracts.Event{T: "human"})
	s.handle(contracts.Event{T: "status", Text: "Bash ls", ToolCall: &contracts.ToolCall{ID: "t1", Tool: "Bash", State: contracts.ToolCallStarted}})
	s.handle(contracts.Event{T: "tool_done", ToolCall: &contracts.ToolCall{ID: "t1", Tool: "Bash", State: contracts.ToolCallFailed, Error: secret}})
	s.handle(contracts.Event{T: "abandoned"})

	last := f.upserts[len(f.upserts)-1]
	if !slices.Contains(strings.Split(last, "\n"), englishHead(headFail)+" Bash") {
		t.Fatalf("status = %q, want the failed line exactly %q", last, englishHead(headFail)+" Bash")
	}
	for _, sent := range slices.Concat(f.upserts, f.posts, f.reacted, f.unreacted) {
		if strings.Contains(sent, secret) {
			t.Fatalf("sent %q, the tool error text must never reach Discord", sent)
		}
	}
}
