package db

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type intentFenceKey struct{}

// WithIntentFence attaches the authorization check needed by control-plane
// intent writes that use a pool directly, such as a wake notification. The
// check acquires transaction locks; callers must retain them through commit.
func WithIntentFence(ctx context.Context, check func(context.Context, pgx.Tx) error) context.Context {
	return context.WithValue(ctx, intentFenceKey{}, check)
}

// WithoutIntentFence is for delivering durable evidence of an intent that
// already committed, including an ownership transfer that changed its caller.
func WithoutIntentFence(ctx context.Context) context.Context {
	return context.WithValue(ctx, intentFenceKey{}, struct{}{})
}

func checkIntentFence(ctx context.Context, tx pgx.Tx) error {
	if check, ok := ctx.Value(intentFenceKey{}).(func(context.Context, pgx.Tx) error); ok {
		return check(ctx, tx)
	}
	return nil
}

func hasIntentFence(ctx context.Context) bool {
	_, ok := ctx.Value(intentFenceKey{}).(func(context.Context, pgx.Tx) error)
	return ok
}
