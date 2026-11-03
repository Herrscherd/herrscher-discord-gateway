package discord

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"testing/synctest"

	"github.com/Herrscherd/dctl"
)

const noGatewaySocket = "://no-gateway"

type teardownTransport struct {
	release <-chan struct{}
}

func (t teardownTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	<-t.release
	return nil, req.Context().Err()
}

func gatewayWithTeardown(release <-chan struct{}) *Gateway {
	client := dctl.New("t", "chan",
		dctl.WithHTTPClient(&http.Client{Transport: teardownTransport{release: release}}),
		dctl.WithGlobalCommands(),
	)
	return &Gateway{
		run:   lifetimeFrom(context.Background()),
		slash: newSlash(client.Interactions(), nil, noGatewaySocket, "t", nil, nil, testLog()),
	}
}

func TestCloseDoesNotReturnWhileTheSlashRuntimeIsStillStopping(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		g := gatewayWithTeardown(release)
		g.BindSessionControl(nil)
		synctest.Wait()

		closed := make(chan error, 1)
		go func() { closed <- g.Close(context.Background()) }()
		synctest.Wait()

		select {
		case err := <-closed:
			close(release)
			t.Fatalf("Close() = %v while the slash runtime was still stopping", err)
		default:
		}
		close(release)
		if err := <-closed; err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
	})
}

func TestCloseReportsItsContextWhenTheRuntimeIsStillStopping(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		defer close(release)
		g := gatewayWithTeardown(release)
		g.BindSessionControl(nil)
		synctest.Wait()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := g.Close(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Close() = %v, want the context's cancellation", err)
		}
	})
}

func TestCloseWithoutASlashRuntimeReturnsNil(t *testing.T) {
	g := &Gateway{}
	if err := g.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
}
