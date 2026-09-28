package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/meter"
)

type fixedSafeReleaseReadiness struct{ healthy bool }

func (f fixedSafeReleaseReadiness) SafeReleaseReadiness(time.Time) meter.HealthStatus {
	return meter.HealthStatus{Healthy: f.healthy}
}

type recordingSafeReleaseProbe struct {
	err   error
	calls int
}

func (p *recordingSafeReleaseProbe) ProbeSafeRelease(context.Context) error {
	p.calls++
	return p.err
}

type recordingSafeReleaseLease struct {
	stamps int
	ttl    time.Duration
}

func (s *recordingSafeReleaseLease) StampSafeReleaseWorkerLease(_ context.Context, ttl time.Duration) error {
	s.stamps++
	s.ttl = ttl
	return nil
}

func (s *recordingSafeReleaseLease) SafeReleaseWorkerLeaseReady(context.Context) (bool, error) {
	return s.stamps > 0, nil
}

func TestSafeReleaseLeaseRequiresLocalTicksAndBothOperatorProbes(t *testing.T) {
	lease := &recordingSafeReleaseLease{}
	probe := &recordingSafeReleaseProbe{}
	now := time.Now()
	if stamped, err := safeReleaseWorkerLeaseOnce(t.Context(), fixedSafeReleaseReadiness{}, lease, probe, now); err != nil || stamped || probe.calls != 0 {
		t.Fatalf("unhealthy local workers: stamped=%v err=%v probe_calls=%d", stamped, err, probe.calls)
	}
	if stamped, err := safeReleaseWorkerLeaseOnce(t.Context(), fixedSafeReleaseReadiness{true}, lease, nil, now); err == nil || stamped || lease.stamps != 0 {
		t.Fatalf("missing operator probe: stamped=%v err=%v stamps=%d", stamped, err, lease.stamps)
	}
	probe.err = errors.New("recovery credential rejected")
	if stamped, err := safeReleaseWorkerLeaseOnce(t.Context(), fixedSafeReleaseReadiness{true}, lease, probe, now); err == nil || stamped || lease.stamps != 0 {
		t.Fatalf("failed operator probe: stamped=%v err=%v stamps=%d", stamped, err, lease.stamps)
	}
	probe.err = nil
	if stamped, err := safeReleaseWorkerLeaseOnce(t.Context(), fixedSafeReleaseReadiness{true}, lease, probe, now); err != nil || !stamped || lease.stamps != 1 || lease.ttl != safeReleaseLeaseTTL {
		t.Fatalf("healthy operator probe: stamped=%v err=%v stamps=%d ttl=%s", stamped, err, lease.stamps, lease.ttl)
	}
	probe.err = errors.New("operator listener unavailable")
	if stamped, err := safeReleaseWorkerLeaseOnce(t.Context(), fixedSafeReleaseReadiness{true}, lease, probe, now); err == nil || stamped || lease.stamps != 1 {
		t.Fatalf("failed renewal: stamped=%v err=%v stamps=%d", stamped, err, lease.stamps)
	}
	probe.err = nil
	if stamped, err := safeReleaseWorkerLeaseOnce(t.Context(), fixedSafeReleaseReadiness{true}, lease, probe, now); err != nil || !stamped || lease.stamps != 2 {
		t.Fatalf("renewed after recovery: stamped=%v err=%v stamps=%d", stamped, err, lease.stamps)
	}
}
