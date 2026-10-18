package repairhealth

// adr: 20261016
// Repair health reports durable admission and cleanup blockers.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type healthReader func(context.Context, time.Time, string, int) (api.DataRepairHealth, error)

func (f healthReader) DataRepairHealth(ctx context.Context, at time.Time, cursor string, limit int) (api.DataRepairHealth, error) {
	return f(ctx, at, cursor, limit)
}

func emptyHealth(at time.Time) api.DataRepairHealth {
	h := api.DataRepairHealth{ObservedAt: at}
	for _, p := range api.DataRepairHealthPhases() {
		h.Phases = append(h.Phases, api.DataRepairPhaseHealth{Phase: p})
	}
	return h
}

func TestRepairHealthCollectorPreservesEvidenceUntilCompleteRead(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg, "schedd")
	at := time.Now().UTC().Truncate(time.Second)
	old := at.Add(-5 * time.Minute)
	h := emptyHealth(at)
	h.Phases[2].Count, h.Phases[2].OldestPhaseStartedAt = 1, &old
	h.HeldSlots, h.HeldMemoryMB, h.HeldVCPUs, h.HeldCPUMillicores, h.UnavailableTasks = 1, 520, 1, 1000, 1
	var readErr error
	c := Collector{Metrics: m, Store: healthReader(func(ctx context.Context, now time.Time, cursor string, limit int) (api.DataRepairHealth, error) {
		deadline, ok := ctx.Deadline()
		if !ok || deadline.Sub(now) > time.Duration(api.DataRepairHealthQuerySeconds)*time.Second || cursor != "" || limit != 0 {
			t.Fatal("collector must request a bounded summary snapshot")
		}
		return h, readErr
	})}
	check := func(got float64, want float64) {
		t.Helper()
		if got != want {
			t.Fatalf("metric = %v, want %v", got, want)
		}
	}
	check(testutil.ToFloat64(m.lastSuccess), 0)
	if err := c.ObserveOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(testutil.ToFloat64(m.oldest.WithLabelValues("cleanup")), float64(old.Unix()))
	check(testutil.ToFloat64(m.counts.WithLabelValues("cleanup")), 1)
	check(testutil.ToFloat64(m.held.WithLabelValues("memory_mb")), 520)
	check(testutil.ToFloat64(m.unavailable), 1)
	for _, tc := range []string{"read error", "partial snapshot", "missing observation time", "duplicate phases", "missing phase clock"} {
		t.Run(tc, func(t *testing.T) {
			h = emptyHealth(at.Add(time.Minute))
			readErr = nil
			switch tc {
			case "read error":
				readErr = errors.New("private database connection details")
			case "partial snapshot":
				h.Phases = h.Phases[:1]
			case "missing observation time":
				h.ObservedAt = time.Time{}
			case "duplicate phases":
				h.Phases[1] = h.Phases[0]
			case "missing phase clock":
				h.Phases[0].Count = 1
			}
			if c.ObserveOnce(t.Context()) == nil {
				t.Fatal("incomplete observation accepted")
			}
			check(testutil.ToFloat64(m.lastSuccess), float64(at.Unix()))
			check(testutil.ToFloat64(m.held.WithLabelValues("slots")), 1)
			check(testutil.ToFloat64(m.oldest.WithLabelValues("cleanup")), float64(old.Unix()))
		})
	}
	h, readErr = emptyHealth(at.Add(2*time.Minute)), nil
	if err := c.ObserveOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(testutil.ToFloat64(m.counts.WithLabelValues("cleanup")), 0)
	check(testutil.ToFloat64(m.oldest.WithLabelValues("cleanup")), 0)
	check(testutil.ToFloat64(m.held.WithLabelValues("slots")), 0)
	check(testutil.ToFloat64(m.unavailable), 0)
	check(testutil.ToFloat64(m.lastSuccess), float64(h.ObservedAt.Unix()))
	check(testutil.ToFloat64(m.collections.WithLabelValues("error")), 5)
	check(testutil.ToFloat64(m.collections.WithLabelValues("success")), 2)
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		for _, metric := range f.Metric {
			for _, label := range metric.Label {
				if label.GetName() != "phase" && label.GetName() != "resource" && label.GetName() != "outcome" {
					t.Errorf("unbounded label on %s: %s", f.GetName(), label.GetName())
				}
			}
		}
	}
}

func TestRepairHealthCollectorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	calls := 0
	c := Collector{Metrics: NewMetrics(prometheus.NewRegistry(), "schedd"), Store: healthReader(func(ctx context.Context, _ time.Time, _ string, _ int) (api.DataRepairHealth, error) {
		calls++
		return api.DataRepairHealth{}, ctx.Err()
	})}
	c.Run(ctx)
	if calls != 0 {
		t.Fatal("cancelled worker performed another read")
	}
	if err := c.ObserveOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
}
