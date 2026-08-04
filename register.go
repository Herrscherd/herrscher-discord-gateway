package discord

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Herrscherd/dctl"
	"github.com/Herrscherd/herrscher-contracts"
)

// init self-registers the Discord gateway into the global plugin registry. A
// blank import of this package (in the host's generated plugins.go) is enough to
// make the gateway discoverable — no wiring in the host. The factory builds its
// own dctl client from config so the plugin stays self-contained.
func init() {
	contracts.Register(contracts.Plugin{
		Manifest: contracts.Manifest{
			Kind:         "discord",
			Category:     contracts.CategoryGateway,
			Status:       contracts.StatusLive,
			Capabilities: contracts.Capabilities{Reactions: true, SelectMenus: true, Replies: true},
			Config: []contracts.Setting{
				{Key: "token", Env: "DISCORD_BOT_TOKEN", Help: "Discord bot token", Required: true},
				{Key: "owner", Env: "DISCORD_USER_ID", Help: "Discord user id the bot obeys (it reads everyone, acts only for this user)", Required: true},
				{Key: "verbosity", Env: "DISCORD_VERBOSITY", Help: "default for how much of a turn shows in-channel, overridable per conversation with /verbosity: silent (default; the answer and nothing else) | quiet (adds a live list of tool names) | actions (adds each tool's detail) | full (adds the assistant's text)"},
				{Key: "context_messages", Env: "DISCORD_CONTEXT_MESSAGES", Help: "how many prior channel messages to carry as context (default 30)"},
				{Key: "playbook", Env: "DISCORD_PLAYBOOK", Help: "skill name a new session is told to follow (default pr-job)"},
			},
		},
		Gateway: NewGatewaySet,
	})
}

// NewGatewaySet builds the Discord channel from config: it wires the outbound
// gateway, the read/status reader, the channel admin and the reachability prober.
func NewGatewaySet(ctx context.Context, cfg contracts.PluginConfig) (contracts.GatewaySet, error) {
	token := cfg.Get("token")
	owner := cfg.Get("owner")
	if owner == "" {
		return contracts.GatewaySet{}, fmt.Errorf("discord gateway: owner (DISCORD_USER_ID) is required — without it the bot would obey everyone")
	}
	// No default channel: the bot is bound to an operator, not to a room, and it
	// always answers in the conversation it was addressed in. Global commands for
	// the same reason — the bot follows its owner across every server it is in,
	// so its slash surface cannot belong to one of them.
	c := dctl.New(token, "", dctl.WithGlobalCommands())
	gw := NewGateway(discordClient{c})
	plat := NewPlatform(c)
	binds := newBindStore(bindStorePath())
	plat.binds = binds

	// One shared set of per-conversation renderers: the gateway feeds it routed
	// events (EmitTo) and the router records the triggering message id for the ACK
	// of the conversation that message belongs to.
	//
	// DISCORD_VERBOSITY is only the default a conversation falls back on: `/verbosity`
	// retunes one room without touching the others and without a daemon restart,
	// so the level is read from the store on every turn, not captured here.
	level := verbositySetting(cfg.Get("verbosity"))
	s := newSinks(ctx, renderAdapter{plat}, func(conv string) string {
		if v := binds.Level(conv); v != "" {
			return v
		}
		return level
	}, owner)
	gw.sinks = s
	plat.sinks = s

	appID, err := c.Interactions().AppID(ctx)
	if err != nil {
		return contracts.GatewaySet{}, fmt.Errorf("discord gateway: resolve application id: %w", err)
	}

	// The slash surface lives entirely in the plugin: it builds its own dctl
	// command catalog + allow store and only crosses the boundary through the
	// neutral SessionControl seam bound later (BindSessionControl).
	gw.slash = newSlash(ctx, c.Interactions(), c.Components(), token, newAllowStore(allowStorePath()), binds)
	// The router reads the controller through a closure because it is bound after
	// the plugin is built (BindSessionControl), not before.
	gw.slash.router = newRouter(
		func() contracts.SessionControl { return gw.slash.ctrl },
		discordClient{c}, binds, s,
		routerConfig{
			owner:           owner,
			appID:           appID,
			contextMessages: intSetting(cfg.Get("context_messages"), 30),
			playbook:        strSetting(cfg.Get("playbook"), "pr-job"),
		},
	)
	return contracts.GatewaySet{
		Gateway: gw,
		Reader:  plat,
		Admin:   NewChannelAdmin(c),
		Prober:  NewProber(c),
	}, nil
}

// stateDir is the gateway's own data dir: beside the daemon state
// (DCTL_STATE_DIR) so it shares the instance's data dir, falling back to
// ~/.config/dctl.
func stateDir() string {
	dir := os.Getenv("DCTL_STATE_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config", "dctl")
	}
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

// allowStorePath is where the gateway persists its permission store.
func allowStorePath() string { return filepath.Join(stateDir(), "discord-allow.json") }

// bindStorePath sits beside the allow store, under the same state dir.
func bindStorePath() string { return filepath.Join(stateDir(), "discord-router.json") }

// intSetting parses a numeric setting, falling back to def for empty or invalid
// values — a typo must not silently disable channel context.
func intSetting(v string, def int) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

// knownLevel reports whether v is one of the four render levels.
func knownLevel(v string) bool {
	switch v {
	case levelSilent, levelQuiet, levelActions, levelFull:
		return true
	}
	return false
}

// verbositySetting maps the configured render level onto the four the renderer
// knows, falling back to silent for anything else. Silent is the only safe
// default: the bot is meant to be pinged in channels other people read, where
// a live list of tools, paths and shell commands is both noise and machine
// layout. Showing more than the answer has to be asked for, and a typo must not
// turn it on by accident.
func verbositySetting(v string) string {
	if knownLevel(v) {
		return v
	}
	return defaultLevel
}

func strSetting(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
