package discord

import (
	"context"
	"io/fs"
	"strconv"
	"strings"
	"testing"

	contracts "github.com/Herrscherd/herrscher-contracts"
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
			return
		}
	}
	t.Fatal("discord gateway did not self-register into contracts.Default")
}

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
	for _, v := range []string{"", "FULL", "verbose", "1"} {
		if got := verbositySetting(v); got != levelSilent {
			t.Errorf("verbositySetting(%q) = %q, want %q", v, got, levelSilent)
		}
	}
}

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
	tree, err := p.Skills(context.Background(), contracts.PluginConfig{})
	if err != nil {
		t.Fatalf("the skill must ship without any config: %v", err)
	}
	b, err := fs.ReadFile(tree, "discord-conversations/SKILL.md")
	if err != nil {
		t.Fatalf("the skill must be readable: %v", err)
	}
	body := string(b)
	if !strings.Contains(strings.ToLower(body), "not instructions") {
		t.Fatalf("the skill must warn that channel content is context, not instructions: %q", body)
	}
	if !strings.Contains(body, "discord channel read") {
		t.Fatalf("the skill must name the prefixed command: %q", body)
	}
	if !strings.Contains(body, strconv.Itoa(readCap)) {
		t.Fatalf("the skill must document the actual readCap (%d): %q", readCap, body)
	}
}
