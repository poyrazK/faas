// applogs_resolver.go — adapter from scheddRouter to the
// AppLogsHandler's logStreamerResolver interface.
//
// Phase 2 / Gate A: the AppLogsHandler wants a per-app dial
// factory that returns a logStreamer (StreamAppLogs only). The
// scheddRouter's ScheddForApp returns a scheddgrpc.ScheddClient
// (full surface); the AppLogsHandler interface only needs the
// StreamAppLogs method.
//
// This adapter does the AppByID hop the AppLogsHandler would
// otherwise have to do itself (it's the gatewayd-side mirror of
// the scheddRouter's per-node resolution: appID → apps.node_id →
// compute_node.schedd_target_url → gRPC client → stream). Keeping
// the hop here means the AppLogsHandler stays a typed proxy and
// doesn't reach into pkg/state.
package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// appLogsScheddResolver implements logStreamerResolver via the
// per-node scheddRouter. One indirection per log-stream dial;
// the resolver's per-node dial cache makes subsequent calls on
// the same node free.
type appLogsScheddResolver struct {
	store  *state.PgStore
	router *scheddRouter
}

// ScheddForApp resolves the owner schedd for appID and returns
// the typed logStreamer the AppLogsHandler consumes. The
// scheddRouter.ScheddForApp signature takes a state.App (it
// needs apps.node_id); this adapter does the AppByID hop so the
// handler doesn't import pkg/state directly. Errors propagate
// to the SSE writer as a degraded envelope.
func (r appLogsScheddResolver) ScheddForApp(ctx context.Context, appID string) (logStreamer, error) {
	app, err := r.store.AppByID(ctx, appID)
	if err != nil {
		return nil, err
	}
	cli, err := r.router.ScheddForApp(ctx, app)
	if err != nil {
		return nil, err
	}
	return cli, nil
}
