package discord

import (
	"testing"

	"github.com/Herrscherd/dctl"
)

func TestTriggerFires(t *testing.T) {
	tr := trigger{owner: "owner1", appID: "app1"}
	owner := dctl.Author{ID: "owner1", Username: "leo"}
	someoneElse := dctl.Author{ID: "other", Username: "sam"}
	botMsg := &referencedMessage{Author: dctl.Author{ID: "app1", Bot: true}}

	cases := []struct {
		name   string
		msg    messageCreate
		direct bool
		want   bool
	}{
		{"owner mentions the bot", messageCreate{Author: owner, Mentions: []dctl.Author{{ID: "app1"}}}, false, true},
		{"owner replies to the bot", messageCreate{Author: owner, Referenced: botMsg}, false, true},
		{"owner says nothing to the bot", messageCreate{Author: owner, Content: "unrelated chatter"}, false, false},
		{"someone else mentions the bot", messageCreate{Author: someoneElse, Mentions: []dctl.Author{{ID: "app1"}}}, false, false},
		{"owner mentions someone else", messageCreate{Author: owner, Mentions: []dctl.Author{{ID: "other"}}}, false, false},
		{"the bot's own message", messageCreate{Author: dctl.Author{ID: "app1", Bot: true}, Mentions: []dctl.Author{{ID: "app1"}}}, false, false},
		{"a bot impersonating the owner id", messageCreate{Author: dctl.Author{ID: "owner1", Bot: true}, Mentions: []dctl.Author{{ID: "app1"}}}, false, false},
		{"owner replies to a human", messageCreate{Author: owner, Referenced: &referencedMessage{Author: someoneElse}}, false, false},
		// In a thread the gateway opened for one job there is nobody else to
		// address, so a bare message is for the bot.
		{"owner speaks in our own thread", messageCreate{Author: owner, Content: "et les tests ?"}, true, true},
		{"someone else speaks in our thread", messageCreate{Author: someoneElse, Content: "coucou"}, true, false},
		{"a bot speaks in our thread", messageCreate{Author: dctl.Author{ID: "owner1", Bot: true}, Content: "x"}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tr.fires(c.msg, c.direct); got != c.want {
				t.Fatalf("fires() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestTriggerNeverFiresWithoutAnOwner(t *testing.T) {
	tr := trigger{appID: "app1"}
	m := messageCreate{Author: dctl.Author{ID: "anyone"}, Mentions: []dctl.Author{{ID: "app1"}}}
	if tr.fires(m, false) {
		t.Fatal("an unconfigured owner must obey nobody, not everybody")
	}
	// The empty-id edge: a payload with no author must not match an empty owner.
	if tr.fires(messageCreate{Mentions: []dctl.Author{{ID: "app1"}}}, false) {
		t.Fatal("an empty owner must not match an empty author id")
	}
	// A thread the gateway opened is still not an authorization to obey anyone.
	if tr.fires(m, true) {
		t.Fatal("an unconfigured owner must obey nobody, thread or not")
	}
}
