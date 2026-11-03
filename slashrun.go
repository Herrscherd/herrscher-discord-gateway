package discord

import (
	"context"
	"errors"
	"fmt"
)

var errGatewayClosed = errors.New("discord gateway closed")

type slashRuntime struct {
	cancel context.CancelCauseFunc
	done   chan struct{}
}

func startSlashRuntime(parent context.Context, handler *slash) *slashRuntime {
	ctx, cancel := context.WithCancelCause(parent)
	runtime := &slashRuntime{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(runtime.done)
		handler.start(ctx)
	}()
	return runtime
}

func (r *slashRuntime) stop(ctx context.Context) error {
	r.cancel(errGatewayClosed)
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("discord gateway: wait for the slash runtime to stop: %w", ctx.Err())
	}
}
