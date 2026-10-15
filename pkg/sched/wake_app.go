package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// wakeAppKey carries the app a wake read, and checked against application
// standards, before it released the app lock to admit. Admission reuses it
// instead of reading the app and its enrollment again moments later. The
// live deployment is still read again in admission, so a deploy cutover in
// between fails closed exactly as before.
type wakeAppKey struct{}

func withWakeApp(ctx context.Context, app state.App) context.Context {
	if app.ID == "" {
		return ctx
	}
	return context.WithValue(ctx, wakeAppKey{}, app)
}

// wakeAppFrom returns the app the wake already read for appID.
func wakeAppFrom(ctx context.Context, appID string) (state.App, bool) {
	app, ok := ctx.Value(wakeAppKey{}).(state.App)
	return app, ok && app.ID == appID
}

// forgetWakeApp ends the reuse once admission has resolved the app, so work
// that outlives this admission on the same context reads the app afresh.
func forgetWakeApp(ctx context.Context) context.Context {
	if _, ok := ctx.Value(wakeAppKey{}).(state.App); !ok {
		return ctx
	}
	return context.WithValue(ctx, wakeAppKey{}, nil)
}
