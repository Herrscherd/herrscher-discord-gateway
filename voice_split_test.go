package discord

import (
	"context"
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestChannelPostSplitsLongTextInsteadOfRefusingIt(t *testing.T) {
	house := &sendRecorder{}
	g := voiceGateway(house)
	in := contracts.Input{Args: map[string]string{"id": "c1", "text": strings.Repeat("a", gatewayMaxLen+10)}}

	if _, err := g.postChannel(context.Background(), in); err != nil {
		t.Fatalf("postChannel() = %v, want the text split into parts", err)
	}
	if len(house.calls) != 2 {
		t.Fatalf("calls = %d, want the text sent in two parts", len(house.calls))
	}
}

func TestChannelReplySplitsLongTextInsteadOfRefusingIt(t *testing.T) {
	house := &sendRecorder{}
	g := voiceGateway(house)
	in := contracts.Input{Args: map[string]string{"id": "c1", "to": "m1", "text": strings.Repeat("a", gatewayMaxLen+10)}}

	if _, err := g.replyMessage(context.Background(), in); err != nil {
		t.Fatalf("replyMessage() = %v, want the text split into parts", err)
	}
	if len(house.calls) != 2 {
		t.Fatalf("calls = %d, want the reply sent in two parts", len(house.calls))
	}
}

func TestRouterPostSplitsLongText(t *testing.T) {
	r, _ := newGuildRouter(t, nil)
	fake, ok := r.c.(*fakeClient)
	if !ok {
		t.Fatalf("router client = %T, want the test client", r.c)
	}

	r.post(context.Background(), "c1", strings.Repeat("a", gatewayMaxLen+10))

	if len(fake.sent) != 2 {
		t.Fatalf("sent = %d messages, want the long text split into two", len(fake.sent))
	}
}
