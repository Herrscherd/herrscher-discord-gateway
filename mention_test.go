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
		name string
		msg  messageCreate
		want bool
	}{
		{"owner mentions the bot", messageCreate{Author: owner, Mentions: []dctl.Author{{ID: "app1"}}}, true},
		{"owner replies to the bot", messageCreate{Author: owner, Referenced: botMsg}, true},
		{"owner says nothing to the bot", messageCreate{Author: owner, Content: "unrelated chatter"}, false},
		{"someone else mentions the bot", messageCreate{Author: someoneElse, Mentions: []dctl.Author{{ID: "app1"}}}, false},
		{"owner mentions someone else", messageCreate{Author: owner, Mentions: []dctl.Author{{ID: "other"}}}, false},
		{"the bot's own message", messageCreate{Author: dctl.Author{ID: "app1", Bot: true}, Mentions: []dctl.Author{{ID: "app1"}}}, false},
		{"a bot impersonating the owner id", messageCreate{Author: dctl.Author{ID: "owner1", Bot: true}, Mentions: []dctl.Author{{ID: "app1"}}}, false},
		{"owner replies to a human", messageCreate{Author: owner, Referenced: &referencedMessage{Author: someoneElse}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tr.fires(c.msg); got != c.want {
				t.Fatalf("fires() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestTriggerNeverFiresWithoutAnOwner(t *testing.T) {
	tr := trigger{appID: "app1"}
	m := messageCreate{Author: dctl.Author{ID: "anyone"}, Mentions: []dctl.Author{{ID: "app1"}}}
	if tr.fires(m) {
		t.Fatal("an unconfigured owner must obey nobody, not everybody")
	}
	// The empty-id edge: a payload with no author must not match an empty owner.
	if tr.fires(messageCreate{Mentions: []dctl.Author{{ID: "app1"}}}) {
		t.Fatal("an empty owner must not match an empty author id")
	}
}
