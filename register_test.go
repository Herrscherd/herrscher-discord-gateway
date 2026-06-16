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
