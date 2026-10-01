// adr: 375
package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type trafficRuntimeObservationStore interface {
	ReadGatewayTrafficEpoch(context.Context, string) (state.GatewayTrafficEpoch, error)
	RegisterGatewayTrafficEpoch(context.Context, string, string, int64) (state.GatewayTrafficEpoch, error)
	ReportGatewayTrafficRuntime(context.Context, state.GatewayTrafficEpoch, state.GatewayTrafficFeatures) error
	RetireGatewayTrafficRuntime(context.Context, state.GatewayTrafficEpoch) error
}

// The caller starts this only after binding its serving listeners and wiring
// handlers. Readiness failures remove the observation; a replaced generation
// exits permanently. Reporting never runs in a request's accounting path.
func startTrafficRuntimeObserver(ctx context.Context, session *trafficRuntimeSession, features state.GatewayTrafficFeatures, ready func() bool, log *slog.Logger) func() {
	if session == nil {
		return func() {}
	}
	observerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runTrafficRuntimeObserver(observerCtx, session, features, ready, log)
	}()
	return func() { cancel(); <-done }
}

func runTrafficRuntimeObserver(ctx context.Context, session *trafficRuntimeSession, features state.GatewayTrafficFeatures, ready func() bool, log *slog.Logger) {
	ticker := time.NewTicker(api.TrafficRuntimeObservationInterval)
	defer ticker.Stop()
	var epoch state.GatewayTrafficEpoch
	defer func() {
		if epoch.Generation == 0 {
			return
		}
		// Preserve context values while allowing bounded retirement after the
		// daemon's serving context is cancelled.
		retireCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.TrafficRuntimeObservationTimeout)
		defer cancel()
		_ = session.store.RetireGatewayTrafficRuntime(retireCtx, epoch)
	}()
	for ctx.Err() == nil {
		opCtx, cancel := context.WithTimeout(ctx, api.TrafficRuntimeObservationTimeout)
		var err error
		epoch, err = session.register(opCtx)
		if err == nil {
			if ready() {
				err = session.store.ReportGatewayTrafficRuntime(opCtx, epoch, features)
			} else {
				err = session.store.RetireGatewayTrafficRuntime(opCtx, epoch)
			}
		}
		cancel()
		session.observeError(err)
		if errors.Is(err, state.ErrGatewayTrafficEpochLost) {
			log.Warn("gateway traffic observation ownership replaced", "node", session.node)
			return
		}
		if err != nil && ctx.Err() == nil {
			log.Warn("gateway traffic observation unavailable", "node", session.node, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
