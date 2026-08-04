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

// The bot follows its owner into every server it is in, so nothing about it is
// scoped to one: no server ever has to be named in config.
func TestNoGuildSettingIsDeclared(t *testing.T) {
	for _, p := range contracts.Default.Gateways() {
		if p.Manifest.Kind != "discord" {
			continue
		}
		for _, s := range p.Manifest.Config {
			if s.Key == "guild" || s.Env == "DISCORD_GUILD_ID" {
				t.Fatalf("manifest still declares a guild setting (%+v); commands are registered globally", s)
			}
		}
	}
}

func TestVerbositySettingFallsBackToSilent(t *testing.T) {
	for _, v := range []string{levelSilent, levelQuiet, levelActions, levelFull} {
		if got := verbositySetting(v); got != v {
			t.Errorf("verbositySetting(%q) = %q, want it kept", v, got)
		}
	}
	// The bot gets pinged in channels other people read: an unset or mistyped
	// level must never be the one that narrates the turn, let alone prints local
	// paths and shell commands.
	for _, v := range []string{"", "FULL", "verbose", "1"} {
		if got := verbositySetting(v); got != levelSilent {
			t.Errorf("verbositySetting(%q) = %q, want %q", v, got, levelSilent)
		}
	}
}
