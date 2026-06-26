package discord

import (
	"context"
	"testing"
)

func TestPlatformReadNotesLastUserToSink(t *testing.T) {
	f := &fakeRender{channel: "c1"}
	s := newSink(context.Background(), f, "full")
	p := &Platform{sink: s} // c left nil: readImpl is injected below

	p.readImpl = func(context.Context, string, int, string) ([]rawMsg, error) {
		return []rawMsg{
			{id: "1", bot: false},
			{id: "2", bot: true},
			{id: "3", bot: false},
		}, nil
	}

	if _, err := p.Read(context.Background(), "c1", 100, ""); err != nil {
		t.Fatal(err)
	}
	if s.lastUser != "3" {
		t.Fatalf("lastUser = %q, want 3", s.lastUser)
	}
}

func TestPlatformSatisfiesRenderClientViaAdapter(t *testing.T) {
	var _ renderClient = (*renderAdapter)(nil)
}
