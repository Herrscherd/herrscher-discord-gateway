package discord

import (
	"context"
	"os"
	"path/filepath"

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
			Capabilities: contracts.Capabilities{Reactions: true, SelectMenus: true, Replies: true},
			Config: []contracts.Setting{
				{Key: "token", Env: "DISCORD_BOT_TOKEN", Help: "Discord bot token", Required: true},
				{Key: "channel", Env: "DISCORD_CHANNEL_ID", Help: "default channel id"},
			},
		},
		Gateway: NewGatewaySet,
	})
}

// NewGatewaySet builds the Discord channel from config: it wires the outbound
// gateway, the read/status reader, the channel admin and the reachability prober.
func NewGatewaySet(ctx context.Context, cfg contracts.PluginConfig) (contracts.GatewaySet, error) {
	token := cfg.Get("token")
	c := dctl.New(token, cfg.Get("channel"))
	gw := NewGateway(discordClient{c})
	// The slash surface lives entirely in the plugin: it builds its own dctl
	// command catalog + allow store and only crosses the boundary through the
	// neutral SessionControl seam bound later (BindSessionControl).
	gw.slash = newSlash(ctx, c.Interactions(), token, newAllowStore(allowStorePath()))
	return contracts.GatewaySet{
		Gateway: gw,
		Reader:  NewPlatform(c),
		Admin:   NewChannelAdmin(c),
		Prober:  NewProber(c),
	}, nil
}

// allowStorePath is where the gateway persists its permission store. It sits
// beside the daemon state (DCTL_STATE_DIR) so it shares the instance's data dir,
// falling back to ~/.config/dctl.
func allowStorePath() string {
	dir := os.Getenv("DCTL_STATE_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config", "dctl")
	}
	_ = os.MkdirAll(dir, 0o700)
	return filepath.Join(dir, "discord-allow.json")
}
