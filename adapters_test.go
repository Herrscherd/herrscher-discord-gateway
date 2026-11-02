package discord

import (
	"context"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestPlatformReadNotesLastUserOnItsOwnChannelSink(t *testing.T) {
	f := &fakeRender{}
	set := newSinks(testRun(), f, staticLevels("full"), "", false, testLog())
	p := &Platform{sinks: set}

	p.readImpl = func(context.Context, string, int, string) ([]rawMsg, error) {
		return []rawMsg{
			{id: "1", bot: false, msg: contracts.Message{ChannelID: "c1"}},
			{id: "2", bot: true, msg: contracts.Message{ChannelID: "c1"}},
			{id: "3", bot: false, msg: contracts.Message{ChannelID: "c1"}},
			{id: "9", bot: false, msg: contracts.Message{ChannelID: "c2"}},
		}, nil
	}

	if _, err := p.Read(context.Background(), "c1", 100, ""); err != nil {
		t.Fatal(err)
	}
	if got := set.at("c1").lastUser; got != (msgRef{ch: "c1", id: "3"}) {
		t.Fatalf("c1 lastUser = %+v, want 3 in c1", got)
	}
	if got := set.at("c2").lastUser; got != (msgRef{ch: "c2", id: "9"}) {
		t.Fatalf("c2 lastUser = %+v, want 9 in c2, the ack must follow the message's own channel", got)
	}
}

func TestPlatformSatisfiesRenderClientViaAdapter(t *testing.T) {
	var _ renderClient = (*renderAdapter)(nil)
}

func TestPlatformStillReadsAnUnboundChannel(t *testing.T) {
	p := &Platform{binds: newBindStore(t.TempDir()+"/router.json", testLog())}
	p.readImpl = func(context.Context, string, int, string) ([]rawMsg, error) {
		return []rawMsg{{id: "1", bot: false, msg: contracts.Message{ChannelID: "c9"}}}, nil
	}

	msgs, err := p.Read(context.Background(), "c9", 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("Read returned %d messages for an unbound channel, want 1", len(msgs))
	}
}
