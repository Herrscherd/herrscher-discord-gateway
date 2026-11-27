package discord

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

type directoryCtrl struct {
	*fakeCtrl

	voices []contracts.AgentVoice
}

func (d directoryCtrl) AgentVoices(string) []contracts.AgentVoice { return d.voices }

func newVoiceRouter(t *testing.T, voices []contracts.AgentVoice, bots *voiceBots) (*router, *fakeCtrl) {
	t.Helper()
	fake := newFakeCtrl()
	fake.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	binds := newBindStore(filepath.Join(t.TempDir(), "router.json"), testLog())
	cfg := routerConfig{
		appID:           "app1",
		voices:          bots,
		contextMessages: 0,
		access:          accessSettings{users: newIDSet("*")},
		address:         defaultAddress("app1"),
		scope:           defaultScope(),
	}
	ctrl := directoryCtrl{fakeCtrl: fake, voices: voices}
	r := newRouter(func() contracts.SessionControl { return ctrl }, &fakeClient{}, binds,
		newSinks(testRun(), &fakeRender{}, staticLevels("silent"), "", false, testMessages(), testLog()), testMessages(), testLog(), cfg)
	requireNoError(t, r.binds.SetLastRepo("local:herrscher"))
	return r, fake
}

func TestHouseDropsAMessageThatMentionsOnlyAnAgentBot(t *testing.T) {
	bots := newVoiceBots(nil)
	bots.register("bob", nil, "bot-app")
	r, ctrl := newVoiceRouter(t, nil, bots)

	msg := guildPing("u1", "<@bot-app> fix the login bug", false)
	msg.Mentions = []dctl.Author{{ID: "bot-app"}}
	r.onMessage(context.Background(), msg)

	if len(ctrl.created) != 0 {
		t.Fatalf("house answered a message meant for an agent bot: %+v", ctrl.created)
	}
}

func TestTextMentionAnswersAsTheTokenlessAgent(t *testing.T) {
	voices := []contracts.AgentVoice{{Agent: "bob", Reply: contracts.ReplyOnMention}}
	r, ctrl := newVoiceRouter(t, voices, newVoiceBots(nil))

	r.onMessage(context.Background(), guildPing("u1", "hey @bob fix the login bug", false))

	if len(ctrl.created) != 1 {
		t.Fatalf("created = %+v, want one session for the mentioned agent", ctrl.created)
	}
	got := ctrl.created[0]
	if got.Agent != "bob" || got.ChannelID != "c1@bob" {
		t.Fatalf("session = %+v, want agent bob answering in c1@bob", got)
	}
}

func TestNeverRuleNeverAnswers(t *testing.T) {
	voices := []contracts.AgentVoice{{Agent: "bob", Reply: contracts.ReplyNever}}
	r, ctrl := newVoiceRouter(t, voices, newVoiceBots(nil))

	r.onMessage(context.Background(), guildPing("u1", "@bob fix the login bug", false))

	if len(ctrl.created) != 0 {
		t.Fatalf("a never agent answered: %+v", ctrl.created)
	}
}

func TestChannelsRuleAnswersEveryMessageOfItsChannel(t *testing.T) {
	voices := []contracts.AgentVoice{{Agent: "bob", Reply: contracts.ReplyInChannels, Channels: []string{"c1"}}}
	r, ctrl := newVoiceRouter(t, voices, newVoiceBots(nil))

	r.onMessage(context.Background(), guildPing("u1", "no mention in this one", false))
	if len(ctrl.created) != 1 || ctrl.created[0].Agent != "bob" {
		t.Fatalf("created = %+v, want bob to answer a plain message of c1", ctrl.created)
	}

	other := guildPing("u1", "no mention, another channel", false)
	other.ChannelID = "c2"
	r.onMessage(context.Background(), other)
	if len(ctrl.created) != 1 {
		t.Fatalf("created = %+v, want no answer outside the listed channel", ctrl.created)
	}
}

func TestAgentBotAnswersADirectMessageOnItsOwnSocket(t *testing.T) {
	bots := newVoiceBots(nil)
	bots.register("bob", nil, "bot-app")
	voices := []contracts.AgentVoice{{Agent: "bob", Reply: contracts.ReplyOnMention}}
	r, ctrl := newVoiceRouter(t, voices, bots)

	dm := messageCreate{ID: "m1", ChannelID: "dm1", Content: "fix the login bug", Author: dctl.Author{ID: "u1", Username: "u1"}}
	r.onAgentMessage(context.Background(), "bob", dm)

	if len(ctrl.created) != 1 || ctrl.created[0].ChannelID != "dm1@bob" {
		t.Fatalf("created = %+v, want bob to answer the direct message in dm1@bob", ctrl.created)
	}
}

func TestAgentBotIgnoresAChannelMessageThatDoesNotAddressIt(t *testing.T) {
	bots := newVoiceBots(nil)
	bots.register("bob", nil, "bot-app")
	voices := []contracts.AgentVoice{{Agent: "bob", Reply: contracts.ReplyOnMention}}
	r, ctrl := newVoiceRouter(t, voices, bots)

	r.onAgentMessage(context.Background(), "bob", guildPing("u1", "a message for someone else", false))

	if len(ctrl.created) != 0 {
		t.Fatalf("bob answered a message that did not address it: %+v", ctrl.created)
	}
}

func TestLoopGuardIgnoresOurOwnBotsWhateverAllowBotsSays(t *testing.T) {
	bots := newVoiceBots(nil)
	bots.register("bob", nil, "bot-app")
	policy := &accessPolicy{selfID: "app1", voices: bots, bots: botsAll, users: newIDSet("*")}

	ownAgent := messageCreate{ChannelID: "c1", Author: dctl.Author{ID: "bot-app", Bot: true}}
	ownHouse := messageCreate{ChannelID: "c1", Author: dctl.Author{ID: "app1", Bot: true}}
	foreign := messageCreate{ChannelID: "c1", Author: dctl.Author{ID: "other-bot", Bot: true}}

	if policy.permits(ownAgent, "", true) || policy.permits(ownHouse, "", true) {
		t.Fatal("a message written by one of our bots was accepted")
	}
	if !policy.permits(foreign, "", true) {
		t.Fatal("a foreign bot message was refused although allow_bots is all")
	}
}
