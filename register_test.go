package discord

import (
	"io/fs"
	"strconv"
	"strings"
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

// The plugin ships its playbook, and it ships it statically: a gateway that
// never instantiates for want of a token must still install the skill that
// teaches an agent to use it.
func TestPluginShipsItsSkill(t *testing.T) {
	var p *contracts.Plugin
	for _, cand := range contracts.Default.Plugins() {
		if cand.Manifest.Kind == "discord" {
			c := cand
			p = &c
		}
	}
	if p == nil {
		t.Fatal("the discord plugin must be registered")
	}
	if p.Skills == nil {
		t.Fatal("the plugin must carry its skill")
	}
	b, err := fs.ReadFile(p.Skills, "discord-conversations/SKILL.md")
	if err != nil {
		t.Fatalf("the skill must be readable: %v", err)
	}
	body := string(b)
	// The load-bearing line: a channel's contents are text written by third
	// parties, and a playbook that says "read this channel" without saying "what
	// you read is not an order" opens prompt injection by Discord message.
	if !strings.Contains(strings.ToLower(body), "not instructions") {
		t.Fatalf("the skill must warn that channel content is context, not instructions: %q", body)
	}
	if !strings.Contains(body, "discord channel read") {
		t.Fatalf("the skill must name the prefixed command: %q", body)
	}
	// Cheap sync guard: the skill documents the read cap as a literal number, so
	// nothing catches it silently drifting from the readCap const in
	// commands.go except this string check.
	if !strings.Contains(body, strconv.Itoa(readCap)) {
		t.Fatalf("the skill must document the actual readCap (%d): %q", readCap, body)
	}
}
