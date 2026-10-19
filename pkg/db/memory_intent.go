package db

import "context"

type memoryIntentKey struct{}

func WithMemoryIntentFence(ctx context.Context, lock func(context.Context) (func(), error)) context.Context {
	return context.WithValue(ctx, memoryIntentKey{}, lock)
}

// LockMemoryIntent must be acquired after the independent catalogue mutex,
// matching promotion callbacks that hold that catalogue while writing state.
func LockMemoryIntent(ctx context.Context) (func(), error) {
	if lock, ok := ctx.Value(memoryIntentKey{}).(func(context.Context) (func(), error)); ok {
		return lock(ctx)
	}
	return func() {}, nil
}
