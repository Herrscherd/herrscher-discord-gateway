package discord

import (
	"testing"

	"github.com/Herrscherd/dctl"
)

func TestAMentionAddressesTheBot(t *testing.T) {
	p := defaultAddress("app1")
	m := guildMsg("u1")
	m.Mentions = []dctl.Author{{ID: "app1"}}
	if !p.addressed(m, chatChannel, "") {
		t.Fatal("an explicit mention did not address the bot")
	}
}

func TestARawMentionAddressesTheBot(t *testing.T) {
	p := defaultAddress("app1")
	m := guildMsg("u1")
	m.Content = "hey <@!app1> regarde ça"
	if p.mentioned(m) {
		t.Fatal("a non-numeric id matched the raw mention pattern")
	}
	m.Content = "hey <@123> regarde ça"
	p.appID = "123"
	if !p.mentioned(m) {
		t.Fatal("a mention Discord resolved only in the body was missed")
	}
}

func TestAReplyToTheBotAddressesIt(t *testing.T) {
	p := defaultAddress("app1")
	m := guildMsg("u1")
	m.Referenced = &dctl.Message{Author: dctl.Author{ID: "app1"}}
	if !p.addressed(m, chatChannel, "") {
		t.Fatal("replying to the bot did not address it")
	}
}

func TestABareChannelMessageDoesNotAddressTheBot(t *testing.T) {
	p := defaultAddress("app1")
	if p.addressed(guildMsg("u1"), chatChannel, "") {
		t.Fatal("the bot took ordinary channel chatter as an instruction")
	}
}

func TestADMNeverNeedsAMention(t *testing.T) {
	p := defaultAddress("app1")
	m := guildMsg("u1")
	m.GuildID = ""
	if !p.addressed(m, chatDM, "") {
		t.Fatal("a DM had to mention the bot")
	}
}

func TestAThreadNeedsNoMentionByDefault(t *testing.T) {
	p := defaultAddress("app1")
	if !p.addressed(guildMsg("u1"), chatThread, "c1") {
		t.Fatal("a thread the gateway opened demanded a mention")
	}
	p.threadRequireMention = true
	if p.addressed(guildMsg("u1"), chatThread, "c1") {
		t.Fatal("thread_require_mention did nothing")
	}
}

func TestAFreeResponseChannelNeedsNoMention(t *testing.T) {
	p := defaultAddress("app1")
	p.free = newIDSet("c1")
	if !p.addressed(guildMsg("u1"), chatChannel, "") {
		t.Fatal("a free-response channel still demanded a mention")
	}
}

func TestAFreeResponseChannelCoversItsThreads(t *testing.T) {
	p := defaultAddress("app1")
	p.free, p.threadRequireMention = newIDSet("c1"), true
	m := guildMsg("u1")
	m.ChannelID = "t1"
	if !p.addressed(m, chatThread, "c1") {
		t.Fatal("a thread of a free-response channel demanded a mention")
	}
}

func TestRequireMentionOffAnswersEveryone(t *testing.T) {
	p := defaultAddress("app1")
	p.requireMention = false
	if !p.addressed(guildMsg("u1"), chatChannel, "") {
		t.Fatal("require_mention=false still demanded a mention")
	}
}
