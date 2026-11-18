package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// probeWakeRecordTimeout bounds one ledger write; it runs after the probe's
// response, off the customer path.
const probeWakeRecordTimeout = 5 * time.Second

// platformProbeWakeRecorder writes the ADR-20261116120000 probe wake ledger.
// A failed write is logged and dropped: the wake is then billed like any
// route probe, which is the safe direction.
func platformProbeWakeRecorder(store state.PlatformProbeWakeStore, log *slog.Logger) gateway.PlatformProbeWakeRecorder {
	if store == nil {
		return nil
	}
	return func(ctx context.Context, appID, instanceID string, wokeAt, probeDoneAt time.Time) {
		ctx, cancel := context.WithTimeout(ctx, probeWakeRecordTimeout)
		defer cancel()
		if err := store.RecordPlatformProbeWake(ctx, state.PlatformProbeWake{
			InstanceID: instanceID, AppID: appID, WokeAt: wokeAt, ProbeDoneAt: probeDoneAt,
		}); err != nil && log != nil {
			log.Warn("gatewayd: record platform probe wake", "app", appID, "instance", instanceID, "err", err)
		}
	}
}
