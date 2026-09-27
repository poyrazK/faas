package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	safeReleaseEmergencyInterval = 15 * time.Second
	safeReleaseEmergencyGrace    = 2 * time.Minute
)

// emergencyAbortSweep only stops canaries with traffic. The store rechecks
// both the lease and the exact rollout under database locks before writing.
func emergencyAbortSweep(ctx context.Context, store state.SafeReleaseEmergencyRecoveryStore, log *slog.Logger, observe func(string)) error {
	rows, err := store.ListCanaryInFlight(ctx)
	if err != nil {
		return err
	}
	for _, d := range rows {
		rolloutState := state.NormalizeRolloutState(d.RolloutState)
		if d.Status != state.DeployLive || (rolloutState != "pending" && rolloutState != "rolling_out") ||
			d.CanaryTotalSteps <= 0 || d.CanaryStep >= d.CanaryTotalSteps ||
			d.TrafficPercent <= 0 || state.IsServiceRollout(d) {
			continue
		}
		_, auditID, err := store.AbortCanaryOnExpiredWorkerLease(ctx, d.AppID, d.ID, safeReleaseEmergencyGrace)
		switch {
		case errors.Is(err, state.ErrSafeReleaseLeaseNotExpired):
			return nil
		case errors.Is(err, state.ErrSafeReleaseLeaseMissing):
			return err
		case errors.Is(err, state.ErrNotFound), errors.Is(err, state.ErrRolloutStateInvalid):
			log.Warn("safe release emergency abort candidate changed or has no serving predecessor", "app_id", d.AppID, "deployment_id", d.ID, "err", err)
			observe("skipped")
			continue
		case err != nil:
			log.Error("safe release emergency abort failed", "app_id", d.AppID, "deployment_id", d.ID, "err", err)
			observe("failed")
			continue
		}
		log.Warn("safe release emergency abort restored predecessor", "app_id", d.AppID, "deployment_id", d.ID, "audit_id", auditID)
		observe("aborted")
	}
	return nil
}

func (s *server) runSafeReleaseEmergencyAbort(ctx context.Context) {
	store, ok := s.store.(state.SafeReleaseEmergencyRecoveryStore)
	if !ok {
		s.log.Error("safe release emergency abort store is unavailable")
		return
	}
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "faas_safe_release_emergency_abort_total",
		Help: "APID emergency canary abort outcomes after the worker lease expires.",
	}, []string{"outcome"})
	if s.ops != nil {
		s.ops.Registry().MustRegister(counter)
	}
	observe := func(outcome string) { counter.WithLabelValues(outcome).Inc() }
	ticker := time.NewTicker(safeReleaseEmergencyInterval)
	defer ticker.Stop()
	for {
		if err := emergencyAbortSweep(ctx, store, s.log, observe); err != nil && ctx.Err() == nil {
			s.log.Error("safe release emergency abort sweep failed", "err", err)
			observe("sweep_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
