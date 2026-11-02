package discord

import (
	"context"
	"reflect"
	"testing"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

func TestPrincipalOfNamesTheCallerInAGuildAndInADM(t *testing.T) {
	guild := dctl.Interaction{Member: dctl.Member{User: dctl.Author{ID: "1234"}}}
	if got := principalOf(guild); got != "discord:1234" {
		t.Fatalf("guild principal = %q, want discord:1234", got)
	}
	dm := dctl.Interaction{User: dctl.Author{ID: "1234"}}
	if got := principalOf(dm); got != "discord:1234" {
		t.Fatalf("DM principal = %q, want discord:1234", got)
	}
}

func TestPrincipalOfNamesNobodyWhenThereIsNoUser(t *testing.T) {
	if got := principalOf(dctl.Interaction{}); got != "" {
		t.Fatalf("principal = %q, want nothing", got)
	}
}

func TestAnInteractionNamesItsCallerToTheCore(t *testing.T) {
	r, ctrl, _ := newTestRouter(t)
	ctrl.repos = []contracts.RepoRef{{Name: "herrscher", Local: true}}
	r.onMessage(context.Background(), ownerPing("fix it"))
	s := &slash{log: testLog(), router: r, comp: &fakeAcker{}, allow: testAllowStore(t)}

	s.onInteraction(context.Background(), dctl.Interaction{
		Type:      dctl.InteractionComponent,
		ChannelID: "c1",
		Member:    dctl.Member{User: dctl.Author{ID: "owner1"}},
		Data:      dctl.InteractionData{CustomID: BindCustomID("c1"), Values: []string{"local:herrscher"}},
	})

	if !reflect.DeepEqual(ctrl.principals, []string{"discord:owner1"}) {
		t.Fatalf("principals = %v, want [discord:owner1]", ctrl.principals)
	}
}
