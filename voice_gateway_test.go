package discord

import (
	"context"
	"sync"
	"testing"
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
	"github.com/coder/websocket"
)

func TestCloseStopsTheAgentWebsockets(t *testing.T) {
	connected := make(chan struct{})
	stopped := make(chan struct{})
	var once, stopOnce sync.Once
	url := fakeGateway(t, func(ctx context.Context, conn *websocket.Conn) {
		defer stopOnce.Do(func() { close(stopped) })
		once.Do(func() { close(connected) })
		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"op":10,"d":{"heartbeat_interval":3600000}}`)); err != nil {
			return
		}
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	})

	g, _, _ := gatewayWithTeardown(releasedTeardown())
	g.log = testLog()
	g.voices = newVoiceBots(nil)
	g.wsURL = url
	g.newBot = botsWith(tokenAppIDs{known: "good"})
	voices := []contracts.AgentVoice{{Agent: "bob", Token: botToken(t, "good")}}
	g.slash.attach = func(ctx context.Context) { g.attachAgents(ctx, voices) }
	g.slashRunning = startSlashRuntime(g.run(), g.slash)

	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent websocket never connected")
	}
	if err := g.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Close returned while the agent websocket was still connected")
	}
}
