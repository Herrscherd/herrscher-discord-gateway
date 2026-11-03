package discord

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/Herrscherd/dctl"
	contracts "github.com/Herrscherd/herrscher-contracts"
)

const noGatewaySocket = "://no-gateway"

type teardownTransport struct {
	release <-chan struct{}
	calls   *atomic.Int32
	causes  chan<- error
}

func (tr teardownTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.calls.Add(1)
	<-req.Context().Done()
	select {
	case tr.causes <- context.Cause(req.Context()):
	default:
	}
	<-tr.release
	return nil, req.Context().Err()
}

type answeringAppID struct {
	teardownTransport
}

func (tr answeringAppID) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/users/@me") {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"42"}`)),
			Request:    req,
		}, nil
	}
	return tr.teardownTransport.RoundTrip(req)
}

func gatewayOver(rt http.RoundTripper) *Gateway {
	client := dctl.New("t", "chan",
		dctl.WithHTTPClient(&http.Client{Transport: rt}),
		dctl.WithGlobalCommands(),
	)
	return &Gateway{
		run:   lifetimeFrom(context.Background()),
		slash: newSlash(client.Interactions(), nil, noGatewaySocket, "t", nil, nil, testLog()),
	}
}

func gatewayWithTeardown(release <-chan struct{}) (*Gateway, *atomic.Int32, <-chan error) {
	calls := new(atomic.Int32)
	causes := make(chan error, 1)
	return gatewayOver(teardownTransport{release: release, calls: calls, causes: causes}), calls, causes
}

func releasedTeardown() <-chan struct{} {
	released := make(chan struct{})
	close(released)
	return released
}

type fakeControl struct {
	contracts.SessionControl
}

func TestCloseDoesNotReturnWhileTheSlashRuntimeIsStillStopping(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		g, _, _ := gatewayWithTeardown(release)
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

func TestCloseCancelsTheRuntimeWithGatewayClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g, _, causes := gatewayWithTeardown(releasedTeardown())
		g.BindSessionControl(nil)
		synctest.Wait()

		if err := g.Close(context.Background()); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
		if cause := <-causes; !errors.Is(cause, errGatewayClosed) {
			t.Fatalf("runtime cancelled with %v, want errGatewayClosed", cause)
		}
	})
}

func TestCloseReportsItsContextWhenTheRuntimeIsStillStopping(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		defer close(release)
		g, _, _ := gatewayWithTeardown(release)
		g.BindSessionControl(nil)
		synctest.Wait()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := g.Close(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Close() = %v, want the context's cancellation", err)
		}
	})
}

func TestCloseAfterAnExpiredCloseReturnsNilOnceStopped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		g, _, _ := gatewayWithTeardown(release)
		g.BindSessionControl(nil)
		synctest.Wait()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := g.Close(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("first Close() = %v, want the context's cancellation", err)
		}
		close(release)
		if err := g.Close(context.Background()); err != nil {
			t.Fatalf("second Close() = %v, want nil once the runtime stopped", err)
		}
		if err := g.Close(context.Background()); err != nil {
			t.Fatalf("third Close() = %v, want nil", err)
		}
	})
}

func TestSecondBindStartsNoSecondRuntime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := new(atomic.Int32)
		g := gatewayOver(answeringAppID{teardownTransport{release: releasedTeardown(), calls: calls, causes: make(chan error, 1)}})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		g.run = lifetimeFrom(ctx)
		g.BindSessionControl(nil)
		g.BindSessionControl(nil)
		synctest.Wait()

		if got := calls.Load(); got != 1 {
			t.Fatalf("command syncs = %d, want one runtime", got)
		}
		if err := g.Close(context.Background()); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
	})
}

func TestSecondBindKeepsTheFirstController(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g, _, _ := gatewayWithTeardown(releasedTeardown())
		first, second := &fakeControl{}, &fakeControl{}
		g.BindSessionControl(first)
		g.BindSessionControl(second)

		if g.slash.ctrl != contracts.SessionControl(first) {
			t.Fatal("second bind replaced the controller, want the first one kept")
		}
		if err := g.Close(context.Background()); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
	})
}

func TestBindAfterCloseStartsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g, calls, _ := gatewayWithTeardown(releasedTeardown())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		g.run = lifetimeFrom(ctx)
		if err := g.Close(context.Background()); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
		g.BindSessionControl(nil)
		synctest.Wait()

		if got := calls.Load(); got != 0 {
			t.Fatalf("command syncs = %d after Close, want none", got)
		}
	})
}

func TestConcurrentBindAndCloseLeaveNoRuntimeRunning(t *testing.T) {
	for range lifetimeRounds {
		synctest.Test(t, func(t *testing.T) {
			g, _, _ := gatewayWithTeardown(releasedTeardown())
			bound := make(chan struct{})
			go func() {
				g.BindSessionControl(nil)
				close(bound)
			}()
			closed := make(chan error, 1)
			go func() { closed <- g.Close(context.Background()) }()

			<-bound
			if err := <-closed; err != nil {
				t.Fatalf("Close() = %v, want nil", err)
			}
			if running := g.slashRunning; running != nil {
				select {
				case <-running.done:
				default:
					t.Fatal("a slash runtime outlived Close")
				}
			}
		})
	}
}

func TestCloseWithoutASlashRuntimeReturnsNil(t *testing.T) {
	g := &Gateway{}
	if err := g.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
}
