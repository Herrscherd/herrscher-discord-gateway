package discord

import (
	"context"
	"net/http"
	"testing"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestOwnBotAnswersAfterItRegistersEvenWithAStaleRouteCached(t *testing.T) {
	bots := newVoiceBots(nil)
	voices := []contracts.AgentVoice{{Agent: "bob", Reply: contracts.ReplyOnMention}}
	r, ctrl := newVoiceRouter(t, voices, bots)
	r.onMessage(context.Background(), guildPing("u1", "hello there", false))

	bots.register("bob", nil, "bot-bob")
	msg := guildPing("u1", "<@bot-bob> fix the login bug", false)
	msg.Mentions = []dctl.Author{{ID: "bot-bob", Bot: true}}
	r.onAgentMessage(context.Background(), "bob", msg, false)

	if len(ctrl.created) != 1 || ctrl.created[0].ChannelID != "c1@bob" {
		t.Fatalf("created = %+v, want bob's own bot to answer c1@bob", ctrl.created)
	}
}

func TestHouseDropsAMessageThatMentionsAPendingAgentBot(t *testing.T) {
	bots := newVoiceBots(nil)
	bots.expect([]string{"bob"})
	voices := []contracts.AgentVoice{
		{Agent: "bob", Reply: contracts.ReplyOnMention},
		{Agent: "dave", Reply: contracts.ReplyInChannels, Channels: []string{"c1"}},
	}
	r, ctrl := newVoiceRouter(t, voices, bots)
	r.setHouseContent(true)

	msg := guildPing("u1", "<@bot-bob> fix the login bug", false)
	msg.Mentions = []dctl.Author{{ID: "bot-bob", Bot: true}}
	r.onMessage(context.Background(), msg)

	if len(ctrl.created) != 0 {
		t.Fatalf("created = %+v, want nothing: the message names bob's bot, which is still resolving", ctrl.created)
	}
}

func TestHouseStillAnswersAMessageThatMentionsItselfAndAPendingAgentBot(t *testing.T) {
	bots := newVoiceBots(nil)
	bots.expect([]string{"bob"})
	r, ctrl := newVoiceRouter(t, nil, bots)

	msg := guildPing("u1", "<@app1> and <@bot-bob> fix the login bug", true)
	msg.Mentions = append(msg.Mentions, dctl.Author{ID: "bot-bob", Bot: true})
	r.onMessage(context.Background(), msg)

	if len(ctrl.created) != 1 {
		t.Fatalf("created = %+v, want the house to answer a message that names it", ctrl.created)
	}
}

func TestAPendingAgentBotThatFailsStopsBlockingItsMentions(t *testing.T) {
	bots := newVoiceBots(nil)
	voices := []contracts.AgentVoice{
		{Agent: "bob", Token: botToken(t, "bad")},
		{Agent: "dave", Reply: contracts.ReplyInChannels, Channels: []string{"c1"}},
	}
	g := &Gateway{log: testLog(), voices: bots, newBot: botsWith(tokenAppIDs{known: "good"})}
	g.expectAgents(voices)
	g.bindAgents(context.Background(), voices)

	r, ctrl := newVoiceRouter(t, voices, bots)
	r.setHouseContent(true)
	msg := guildPing("u1", "<@bot-bob> fix the login bug", false)
	msg.Mentions = []dctl.Author{{ID: "bot-bob", Bot: true}}
	r.onMessage(context.Background(), msg)

	if len(ctrl.created) != 1 || ctrl.created[0].ChannelID != "c1@dave" {
		t.Fatalf("created = %+v, want the channels agent to answer once bob's failed resolution has settled", ctrl.created)
	}
}

type slowAppID struct{ release <-chan struct{} }

func (s slowAppID) RoundTrip(req *http.Request) (*http.Response, error) {
	select {
	case <-s.release:
		return jsonReply(`{"id":"42"}`)(req)
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
}

func TestAPendingAgentWithItsOwnTokenIsNeverAnsweredAsTokenless(t *testing.T) {
	release := make(chan struct{})
	bots := newVoiceBots(nil)
	voices := []contracts.AgentVoice{{Agent: "bob", Reply: contracts.ReplyOnMention, Token: botToken(t, "slow")}}
	g := &Gateway{log: testLog(), voices: bots, newBot: botsWith(slowAppID{release})}
	g.expectAgents(voices)
	bindDone := make(chan struct{})
	go func() {
		defer close(bindDone)
		g.bindAgents(context.Background(), voices)
	}()
	t.Cleanup(func() {
		close(release)
		<-bindDone
	})

	r, ctrl := newVoiceRouter(t, voices, bots)
	r.onMessage(context.Background(), guildPing("u1", "@bob hello there", false))

	if len(ctrl.created) != 0 {
		t.Fatalf("created = %+v, want nothing: bob has a token still being checked, so the house must not answer for him as if he had none", ctrl.created)
	}
}

func TestAPendingAgentsTextMentionIsDroppedInEveryChatKind(t *testing.T) {
	cases := []struct {
		name    string
		msg     messageCreate
		ambient bool
		thread  bool
	}{
		{
			name: "dm",
			msg:  messageCreate{ID: "m1", ChannelID: "c1", Content: "@bob hello there", Author: dctl.Author{ID: "u1", Username: "u1"}},
		},
		{
			name:    "ambient channel",
			msg:     guildPing("u1", "@bob hello there", false),
			ambient: true,
		},
		{
			name:   "thread",
			msg:    messageCreate{ID: "m1", ChannelID: "t1", GuildID: "g1", Content: "@bob hello there", Author: dctl.Author{ID: "u1", Username: "u1"}},
			thread: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bots := newVoiceBots(nil)
			bots.expect([]string{"bob"})
			voices := []contracts.AgentVoice{{Agent: "bob", Reply: contracts.ReplyOnMention}}
			r, ctrl := newVoiceRouter(t, voices, bots)
			if tc.ambient {
				requireNoError(t, r.binds.SetMode("c1", ambientMode))
			}
			if tc.thread {
				requireNoError(t, r.binds.BindThread("t1", "t1", "c1", "ch-t1"))
			}

			r.onMessage(context.Background(), tc.msg)

			if len(ctrl.created) != 0 {
				t.Fatalf("created = %+v, want nothing: bob is still resolving, so the house must not answer his text mention", ctrl.created)
			}
		})
	}
}

func TestAPendingAgentDoesNotSilenceAReadyAgentNamedInTheSameMessage(t *testing.T) {
	bots := newVoiceBots(nil)
	bots.expect([]string{"bob"})
	voices := []contracts.AgentVoice{
		{Agent: "bob", Reply: contracts.ReplyOnMention},
		{Agent: "alice", Reply: contracts.ReplyOnMention},
	}
	r, ctrl := newVoiceRouter(t, voices, bots)

	r.onMessage(context.Background(), guildPing("u1", "@bob @alice hi there", false))

	if len(ctrl.created) != 1 || ctrl.created[0].ChannelID != "c1@alice" {
		t.Fatalf("created = %+v, want alice to answer c1@alice and bob to stay silent while he resolves", ctrl.created)
	}
}

func TestAPendingAgentDoesNotBlockACaptureOfAnotherVoice(t *testing.T) {
	cases := []struct {
		name  string
		reply contracts.ReplyRule
	}{
		{name: "pending on mention", reply: contracts.ReplyOnMention},
		{name: "pending never", reply: contracts.ReplyNever},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bots := newVoiceBots(nil)
			bots.expect([]string{"bob"})
			voices := []contracts.AgentVoice{
				{Agent: "bob", Reply: tc.reply},
				{Agent: "alice", Reply: contracts.ReplyInChannels, Channels: []string{"c1"}},
			}
			r, ctrl := newVoiceRouter(t, voices, bots)
			r.setHouseContent(true)

			r.onMessage(context.Background(), guildPing("u1", "@bob hi", false))

			if len(ctrl.created) != 1 || ctrl.created[0].ChannelID != "c1@alice" {
				t.Fatalf("created = %+v, want alice to capture c1 and answer c1@alice while bob resolves", ctrl.created)
			}
		})
	}
}

func TestAPendingNeverAgentDoesNotHoldTheHouseBack(t *testing.T) {
	bots := newVoiceBots(nil)
	bots.expect([]string{"bob"})
	voices := []contracts.AgentVoice{{Agent: "bob", Reply: contracts.ReplyNever}}
	r, ctrl := newVoiceRouter(t, voices, bots)
	requireNoError(t, r.binds.SetMode("c1", ambientMode))

	r.onMessage(context.Background(), guildPing("u1", "@bob hi", false))

	if len(ctrl.created) != 1 || ctrl.created[0].Agent != "" {
		t.Fatalf("created = %+v, want the house to answer in ambient mode: a never agent that resolves keeps no silence on the house", ctrl.created)
	}
}
