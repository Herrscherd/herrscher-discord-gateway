package discord

import (
	"testing"

	"github.com/Herrscherd/herrscher-contracts"
)

func TestSelfRegisteredAsGateway(t *testing.T) {
	for _, p := range contracts.Default.Gateways() {
		if p.Manifest.Kind == "discord" {
			if p.Gateway == nil {
				t.Fatal("registered discord plugin has a nil gateway factory")
			}
			if !p.Manifest.Capabilities.Reactions || !p.Manifest.Capabilities.SelectMenus {
				t.Fatalf("discord manifest dropped capabilities: %+v", p.Manifest.Capabilities)
			}
			// The factory resolves the bot appID over the network, so a no-token
			// build can't invoke it here; building a GatewaySet end to end is the
			// integration check in herrscherd.
			return
		}
	}
	t.Fatal("discord gateway did not self-register into contracts.Default")
}

// The guild is optional (a mono-server bot resolves its own) but must be
// declared, or an operator whose bot joined a second server has no way to say
// which one the commands belong to.
func TestGuildSettingIsDeclaredOptional(t *testing.T) {
	for _, p := range contracts.Default.Gateways() {
		if p.Manifest.Kind != "discord" {
			continue
		}
		for _, s := range p.Manifest.Config {
			if s.Key == "guild" {
				if s.Env != "DISCORD_GUILD_ID" {
					t.Errorf("guild env = %q, want DISCORD_GUILD_ID", s.Env)
				}
				if s.Required {
					t.Error("guild must stay optional: a mono-server bot resolves its own")
				}
				return
			}
		}
		t.Fatal("discord manifest declares no guild setting")
	}
}

func TestClientOptsOnlyWhenGuildSet(t *testing.T) {
	if got := clientOpts(""); got != nil {
		t.Errorf("clientOpts(\"\") = %v, want nil so dctl keeps resolving the sole guild", got)
	}
	if got := clientOpts("g1"); len(got) != 1 {
		t.Errorf("clientOpts(\"g1\") = %v, want one option", got)
	}
}
