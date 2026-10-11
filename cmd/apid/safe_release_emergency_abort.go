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

func servingCanary(d state.Deployment) bool {
	rolloutState := state.NormalizeRolloutState(d.RolloutState)
	return d.Status == state.DeployLive && (rolloutState == "pending" || rolloutState == "rolling_out") &&
		d.CanaryTotalSteps > 0 && d.CanaryStep < d.CanaryTotalSteps &&
		d.TrafficPercent > 0 && !state.IsServiceRollout(d)
}

// emergencyAbortSweep only stops canaries with traffic. The store rechecks
// both the lease and the exact rollout under database locks before writing.
// The returned count is the serving set observed before any aborts, so the
// monitoring loop can expose risk even if recovery later fails.
func emergencyAbortSweep(ctx context.Context, store state.SafeReleaseEmergencyRecoveryStore, log *slog.Logger, observe func(string)) (int, error) {
	rows, err := store.ListCanaryInFlight(ctx)
	if err != nil {
		return 0, err
	}
	serving := 0
	for _, d := range rows {
		if servingCanary(d) {
			serving++
		}
	}
	for _, d := range rows {
		if !servingCanary(d) {
			continue
		}
		_, auditID, err := store.AbortCanaryOnExpiredWorkerLease(ctx, d.AppID, d.ID, safeReleaseEmergencyGrace)
		switch {
		case errors.Is(err, state.ErrSafeReleaseLeaseNotExpired):
			return serving, nil
		case errors.Is(err, state.ErrSafeReleaseLeaseMissing):
			return serving, err
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
	return serving, nil
}

type safeReleaseLeaseMetrics struct {
	ready        prometheus.Gauge
	secondsLeft  prometheus.Gauge
	checkSuccess prometheus.Gauge
	lastCheck    prometheus.Gauge
	serving      prometheus.Gauge
}

func newSafeReleaseLeaseMetrics() safeReleaseLeaseMetrics {
	return safeReleaseLeaseMetrics{
		ready:        prometheus.NewGauge(prometheus.GaugeOpts{Name: "faas_safe_release_worker_lease_ready", Help: "Whether the database worker lease currently admits new canaries."}),
		secondsLeft:  prometheus.NewGauge(prometheus.GaugeOpts{Name: "faas_safe_release_worker_lease_seconds_until_expiry", Help: "Seconds until the worker lease expires, measured with the database clock; negative after expiry."}),
		checkSuccess: prometheus.NewGauge(prometheus.GaugeOpts{Name: "faas_safe_release_worker_lease_check_success", Help: "Whether APID's latest database worker lease observation succeeded."}),
		lastCheck:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "faas_safe_release_worker_lease_last_check_timestamp_seconds", Help: "APID timestamp of the last successful worker lease observation."}),
		serving:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "faas_safe_release_serving_canaries", Help: "Canaries currently serving nonzero traffic according to APID's latest successful sweep."}),
	}
}

func observeSafeReleaseLease(ctx context.Context, store state.SafeReleaseWorkerLeaseHealthStore, metrics safeReleaseLeaseMetrics) error {
	health, err := store.SafeReleaseWorkerLeaseHealth(ctx)
	if err != nil {
		metrics.ready.Set(0)
		metrics.secondsLeft.Set(0)
		metrics.checkSuccess.Set(0)
		return err
	}
	metrics.checkSuccess.Set(1)
	metrics.lastCheck.SetToCurrentTime()
	metrics.secondsLeft.Set(health.SecondsUntilExpiry())
	if health.Ready() {
		metrics.ready.Set(1)
	} else {
		metrics.ready.Set(0)
	}
	return nil
}

func (s *server) runSafeReleaseEmergencyAbort(ctx context.Context) {
	store, ok := s.store.(state.SafeReleaseEmergencyRecoveryStore)
	if !ok {
		s.log.Error("safe release emergency abort store is unavailable")
		return
	}
	healthStore, ok := s.store.(state.SafeReleaseWorkerLeaseHealthStore)
	if !ok {
		s.log.Error("safe release worker lease health store is unavailable")
		return
	}
	checkedStore := bindingCheckedEmergencyRecoveryStore{SafeReleaseEmergencyRecoveryStore: store, server: s}
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "faas_safe_release_emergency_abort_total",
		Help: "APID emergency canary abort outcomes after the worker lease expires.",
	}, []string{"outcome"})
	for _, outcome := range []string{"aborted", "failed", "skipped", "sweep_failed"} {
		counter.WithLabelValues(outcome)
	}
	leaseMetrics := newSafeReleaseLeaseMetrics()
	if s.ops != nil {
		s.ops.Registry().MustRegister(counter, leaseMetrics.ready, leaseMetrics.secondsLeft,
			leaseMetrics.checkSuccess, leaseMetrics.lastCheck, leaseMetrics.serving)
	}
	observe := func(outcome string) { counter.WithLabelValues(outcome).Inc() }
	ticker := time.NewTicker(safeReleaseEmergencyInterval)
	defer ticker.Stop()
	for {
		leaseCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := observeSafeReleaseLease(leaseCtx, healthStore, leaseMetrics); err != nil && ctx.Err() == nil {
			s.log.Warn("safe release worker lease observation failed", "err", err)
		}
		cancel()
		serving, err := emergencyAbortSweep(ctx, checkedStore, s.log, observe)
		if err == nil {
			leaseMetrics.serving.Set(float64(serving))
		} else if ctx.Err() == nil {
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
