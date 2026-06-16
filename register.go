package discord

import (
	"context"

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
		},
		Gateway: NewGatewaySet,
	})
}

// NewGatewaySet builds the full Discord channel from config: it wires the
// outbound gateway, the websocket command source, the read/status reader, the
// channel admin, the command registrar and the reachability prober. The status
// channel comes from config ("status_channel"); appID is resolved up front so
// the responder can edit deferred replies.
func NewGatewaySet(ctx context.Context, cfg contracts.PluginConfig) (contracts.GatewaySet, error) {
	token := cfg.Get("token")
	c := dctl.New(token, cfg.Get("channel"))
	appID, err := c.AppID(ctx)
	if err != nil {
		return contracts.GatewaySet{}, err
	}
	return contracts.GatewaySet{
		Gateway:   NewGateway(c),
		Source:    NewCommandSource(c, token, appID),
		Reader:    NewPlatform(c),
		Admin:     NewChannelAdmin(c),
		Registrar: NewRegistrar(c),
		Prober:    NewProber(c),
	}, nil
}
