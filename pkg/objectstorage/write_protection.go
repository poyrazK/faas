package objectstorage

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
)

// ObjectWriteProtectionProvider explicitly opts into consuming immutable write
// snapshots on every tracked dispatch and readback. Recovery ignores enrollment.
type ObjectWriteProtectionProvider interface {
	BindObjectWriteProtection(context.Context, state.ObjectWriteProtectionSnapshot) (context.Context, error)
	ConfirmObjectWriteProtection(context.Context, string, string, string, string, int64, bool, string) (string, error)
}
type writeProtectionKey struct{}
type writeProtectionRecorderKey struct{}
type writeChecksumKey struct{}

func WithObjectWriteProtection(ctx context.Context, provider any, snapshot state.ObjectWriteProtectionSnapshot, before func(context.Context) error) (context.Context, error) {
	if snapshot.Empty() {
		return ctx, nil
	}
	p, ok := provider.(ObjectWriteProtectionProvider)
	if !ok || !snapshot.Valid() {
		return ctx, ErrConfiguration
	}
	ctx = context.WithValue(ctx, writeProtectionRecorderKey{}, before)
	return p.BindObjectWriteProtection(ctx, snapshot.Clone())
}
func capturedWriteProtection(ctx context.Context) state.ObjectWriteProtectionSnapshot {
	p, _ := ctx.Value(writeProtectionKey{}).(state.ObjectWriteProtectionSnapshot)
	return p.Clone()
}
func beforeProtectionRead(ctx context.Context) error {
	if before, ok := ctx.Value(writeProtectionRecorderKey{}).(func(context.Context) error); ok && before != nil {
		return before(ctx)
	}
	return ctx.Err()
}

// The broker has already bounded and spooled the body, so it can bind MD5
// without buffering an additional copy. It never exposes this native URL.
func WithObjectWriteChecksum(ctx context.Context, checksum string) context.Context {
	return context.WithValue(ctx, writeChecksumKey{}, checksum)
}
