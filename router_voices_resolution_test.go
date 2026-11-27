package discord

import (
	"context"
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
