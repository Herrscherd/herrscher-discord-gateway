package discord

import (
	"context"
	"errors"
	"testing"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

type readCounter struct {
	*fakeClient
	channels []string
}

func (c *readCounter) ReadMessages(ctx context.Context, ch string, limit int, after string) ([]dctl.Message, error) {
	c.channels = append(c.channels, ch)
	return c.fakeClient.ReadMessages(ctx, ch, limit, after)
}

func chatterWithoutTarget(n int) []dctl.Message {
	out := make([]dctl.Message, 0, n)
	for i := range n {
		out = append(out, dctl.Message{ID: "other" + string(rune('a'+i)), ChannelID: "c1", Content: "bavardage"})
	}
	return out
}

func TestBlankMessageWhoseContentCannotBeReadIsIgnored(t *testing.T) {
	cases := []struct {
		name    string
		agent   string
		pending bool
		client  *fakeClient
	}{
		{
			name:    "read error with a pending name",
			agent:   "bob",
			pending: true,
			client:  &fakeClient{readErr: errors.New("rate limited")},
		},
		{
			name:    "target outside the window with a pending name",
			agent:   "bob",
			pending: true,
			client:  &fakeClient{read: chatterWithoutTarget(hydrateWindow)},
		},
		{
			name:   "target outside the window with a ready tokenless name",
			agent:  "alice",
			client: &fakeClient{read: chatterWithoutTarget(hydrateWindow)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bots := newVoiceBots(nil)
			if tc.pending {
				bots.expect([]string{tc.agent})
			}
			voices := []contracts.AgentVoice{{Agent: tc.agent, Reply: contracts.ReplyOnMention}}
			r, ctrl := newVoiceRouter(t, voices, bots)
			requireNoError(t, r.binds.SetMode("c1", ambientMode))
			r.c = tc.client

			r.onMessage(context.Background(), guildPing("u1", "", false))

			if len(ctrl.created) != 0 {
				t.Fatalf("house answered a message whose content it could not read: %+v", ctrl.created)
			}
		})
	}
}

func TestBlankMessageIsReadOnlyWhenItCanMatter(t *testing.T) {
	cases := []struct {
		name    string
		voices  []contracts.AgentVoice
		setup   func(*voiceBots)
		ambient bool
		mention bool
		reads   int
	}{
		{name: "unaddressed with no voices", reads: 0},
		{
			name:   "unaddressed with only an own voice",
			voices: []contracts.AgentVoice{{Agent: "bob", Reply: contracts.ReplyOnMention}},
			setup:  func(b *voiceBots) { b.register("bob", nil, "bot-app") },
			reads:  0,
		},
		{name: "mentioned", mention: true, reads: 1},
		{name: "ambient channel", ambient: true, reads: 1},
		{
			name:   "token-less voice",
			voices: []contracts.AgentVoice{{Agent: "alice", Reply: contracts.ReplyOnMention}},
			reads:  1,
		},
		{
			name:   "pending agent",
			voices: []contracts.AgentVoice{{Agent: "bob", Reply: contracts.ReplyOnMention}},
			setup:  func(b *voiceBots) { b.expect([]string{"bob"}) },
			reads:  1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bots := newVoiceBots(nil)
			if tc.setup != nil {
				tc.setup(bots)
			}
			r, _ := newVoiceRouter(t, tc.voices, bots)
			if tc.ambient {
				requireNoError(t, r.binds.SetMode("c1", ambientMode))
			}
			counter := &readCounter{fakeClient: &fakeClient{}}
			r.c = counter

			r.onMessage(context.Background(), guildPing("u1", "", tc.mention))

			if len(counter.channels) != tc.reads {
				t.Fatalf("reads = %d, want %d (channels %v)", len(counter.channels), tc.reads, counter.channels)
			}
		})
	}
}

func TestAgentSocketReadsTheBlankMessageInTheAgentsChannel(t *testing.T) {
	bots := newVoiceBots(nil)
	bots.register("bob", nil, "bot-app")
	voices := []contracts.AgentVoice{{Agent: "bob", Reply: contracts.ReplyOnMention}}
	r, _ := newVoiceRouter(t, voices, bots)
	counter := &readCounter{fakeClient: &fakeClient{read: []dctl.Message{{ID: "m-u1", ChannelID: "c1", Content: "hi"}}}}
	r.c = counter
	msg := guildPing("u1", "", false)
	msg.Mentions = []dctl.Author{{ID: "bot-app"}}

	r.onAgentMessage(context.Background(), "bob", msg, false)

	if len(counter.channels) != 1 || counter.channels[0] != "c1@bob" {
		t.Fatalf("read channels = %v, want [c1@bob] so the agent's own bot reads", counter.channels)
	}
}
