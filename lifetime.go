package discord

import "context"

type lifetime func() context.Context

func lifetimeFrom(ctx context.Context) lifetime {
	return func() context.Context { return ctx }
}
