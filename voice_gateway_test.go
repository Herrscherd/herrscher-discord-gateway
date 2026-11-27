package discord

import (
	"context"
	"sync"
	"testing"
	"time"

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
	g.slash.agents = []*ws{newWS(url, "t", testLog(), nil, func(context.Context, messageCreate) {}, nil)}
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
