package state

import (
	"context"
	"time"
)

// ConfigGuardCursor resumes a sweep after its last evaluated guard. Quiet
// guards retain their change time; they must not occupy every batch forever.
type ConfigGuardCursor struct {
	ChangedAt time.Time
	ID        string
}

type configGuardCursorKey struct{}

func WithConfigGuardCursor(ctx context.Context, cursor ConfigGuardCursor) context.Context {
	return context.WithValue(ctx, configGuardCursorKey{}, cursor)
}

func configGuardCursor(ctx context.Context) ConfigGuardCursor {
	cursor, _ := ctx.Value(configGuardCursorKey{}).(ConfigGuardCursor)
	return cursor
}
