package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// routeTargetResolver resolves a kind=route rule's target slug to the app the
// gateway substitutes. Targets resolve through the public-visibility check:
// an internal app is off the public edge entirely, and resolving targets with
// toApp let any same-account route rule serve one on a public host.
//
// Error classification (review fix R2): state.ErrNotFound is a clean miss
// (the target app row was deleted) — silent fall-through. Anything else is a
// real error (DB outage, pool exhaustion, ctx cancel) and gets logged at WARN
// + audited so a dashboard operator can distinguish a noisy transient from a
// sustained backend failure.
func routeTargetResolver(store state.Store, log *slog.Logger, audit *gatewaydEdgeRulesAud) func(context.Context, string) (gateway.App, bool) {
	loaderError := func(ctx context.Context, slug, msg string, err error) {
		if log != nil {
			log.Warn(msg, "slug", slug, "err", err)
		}
		if audit != nil {
			subject := slug
			audit.Emit(ctx, "edge_rule.route_loader_error", &subject, map[string]any{
				"slug": slug,
				"err":  err.Error(),
			})
		}
	}
	return func(ctx context.Context, slug string) (gateway.App, bool) {
		app, err := store.AppBySlug(ctx, slug)
		if err != nil {
			if !errors.Is(err, state.ErrNotFound) {
				loaderError(ctx, slug, "edge rule target AppBySlug failed", err)
			}
			return gateway.App{}, false
		}
		resolved, ok, err := (pgRouter{store: store}).toPublicApp(ctx, app)
		if err != nil {
			loaderError(ctx, slug, "edge rule target account lookup failed", err)
			return gateway.App{}, false
		}
		return resolved, ok
	}
}
