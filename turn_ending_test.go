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
