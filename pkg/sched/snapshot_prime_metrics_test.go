package sched

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type primeObservationReaderFunc func(context.Context, string) (state.SnapshotPrimeSnapshot, error)

func (f primeObservationReaderFunc) SnapshotPrimeSnapshot(ctx context.Context, node string) (state.SnapshotPrimeSnapshot, error) {
	return f(ctx, node)
}

func primeGauges(t *testing.T, reg *prometheus.Registry) map[string]float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, f := range families {
		for _, m := range f.Metric {
			if m.Gauge == nil {
				continue
			}
			key := f.GetName()
			for _, l := range m.Label {
				if l.GetName() == "class" {
					key += "/" + l.GetValue()
				}
			}
			out[key] = m.Gauge.GetValue()
		}
	}
	return out
}

// adr: 20261017122043000 — Read-only prime handoff evidence and independent collection health.
func TestSnapshotPrimeMetricsFreshnessAndUnknownHistory(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newSnapshotPrimeMetrics(reg)
	now := time.Unix(1700000000, 0).UTC()
	m.now = func() time.Time { return now }
	const count = "schedd_snapshot_prime_pending/waiting"
	const oldest = "schedd_snapshot_prime_oldest_attempt_timestamp_seconds/waiting"
	const unknown = "schedd_snapshot_prime_age_unknown/waiting"
	const success = "schedd_snapshot_prime_collection_success"
	const last = "schedd_snapshot_prime_collection_last_success_timestamp_seconds"
	if _, exists := primeGauges(t, reg)[count]; exists {
		t.Fatal("startup fabricated empty snapshot")
	}
	s := state.SnapshotPrimeSnapshot{
		state.SnapshotPrimeWaiting: state.SnapshotPrimeCohort{Pending: 3, UnknownAge: 1, OldestAt: now.Add(-45 * time.Minute)},
		state.SnapshotPrimeUnknown: state.SnapshotPrimeCohort{Pending: 1, UnknownAge: 1},
	}
	reader := primeObservationReaderFunc(func(ctx context.Context, node string) (state.SnapshotPrimeSnapshot, error) {
		if node != "node-a" {
			t.Errorf("node=%q", node)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > api.ObsSnapshotPrimeCollectionBudget {
			t.Error("unbounded observation read")
		}
		return s, nil
	})
	if err := m.refresh(t.Context(), reader, "node-a"); err != nil {
		t.Fatal(err)
	}
	got := primeGauges(t, reg)
	if len(got) != 21 || got[count] != 3 || got[unknown] != 1 || got[oldest] != primeUnix(s[state.SnapshotPrimeWaiting].OldestAt) || got["schedd_snapshot_prime_pending/unknown"] != 1 || got["schedd_snapshot_prime_oldest_attempt_timestamp_seconds/unknown"] != 0 || got[success] != 1 {
		t.Fatal(got)
	}
	observed := got[last]
	now = now.Add(api.ObsSnapshotPrimeFreshnessMaxAge)
	if _, exists := primeGauges(t, reg)[count]; !exists {
		t.Fatal("exact freshness ceiling expired")
	}
	now = now.Add(time.Nanosecond)
	if _, exists := primeGauges(t, reg)[count]; exists {
		t.Fatal("expired data remained visible")
	}
	broken := primeObservationReaderFunc(func(context.Context, string) (state.SnapshotPrimeSnapshot, error) {
		return state.SnapshotPrimeSnapshot{}, errors.New("read failed")
	})
	if err := m.refresh(t.Context(), broken, "node-a"); err == nil {
		t.Fatal("failure hidden")
	}
	got = primeGauges(t, reg)
	if got[success] != 0 || got[last] != observed {
		t.Fatal("failure renewed success", got)
	}
	if _, exists := got[count]; exists {
		t.Fatal("failure fabricated an empty or cached snapshot")
	}
	s = state.SnapshotPrimeSnapshot{}
	if err := m.refresh(t.Context(), reader, "node-a"); err != nil {
		t.Fatal(err)
	}
	got = primeGauges(t, reg)
	if value, exists := got[count]; !exists || value != 0 || got[oldest] != 0 || got[unknown] != 0 {
		t.Fatal("known empty lost", got)
	}
	now = now.Add(-time.Second)
	if _, exists := primeGauges(t, reg)[count]; exists {
		t.Fatal("future observation accepted")
	}
}

func TestSnapshotPrimeMetricsRejectInconsistentEvidence(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	for name, p := range map[string]state.SnapshotPrimeCohort{
		"negative count":             {Pending: -1},
		"negative unknown":           {Pending: 1, UnknownAge: -1},
		"unknown exceeds count":      {Pending: 1, UnknownAge: 2},
		"missing known time":         {Pending: 1},
		"timestamp for empty":        {OldestAt: now},
		"timestamp for unknown only": {Pending: 1, UnknownAge: 1, OldestAt: now},
		"future phase":               {Pending: 1, OldestAt: now.Add(time.Second)},
		"pre epoch":                  {Pending: 1, OldestAt: time.Unix(-1, 0)},
	} {
		t.Run(name, func(t *testing.T) {
			reg := prometheus.NewPedanticRegistry()
			m := newSnapshotPrimeMetrics(reg)
			m.now = func() time.Time { return now }
			r := primeObservationReaderFunc(func(context.Context, string) (state.SnapshotPrimeSnapshot, error) {
				return state.SnapshotPrimeSnapshot{state.SnapshotPrimeActive: p}, nil
			})
			if err := m.refresh(t.Context(), r, ""); err == nil {
				t.Fatal("accepted inconsistent phase")
			}
			if _, exists := primeGauges(t, reg)["schedd_snapshot_prime_pending/waiting"]; exists {
				t.Fatal("invalid snapshot partially emitted")
			}
		})
	}
}

func TestSnapshotPrimeMetricsReadStartAndCanceledRead(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newSnapshotPrimeMetrics(reg)
	now := time.Unix(1700000000, 0).UTC()
	start := now
	m.now = func() time.Time { return now }
	r := primeObservationReaderFunc(func(context.Context, string) (state.SnapshotPrimeSnapshot, error) {
		now = now.Add(time.Second)
		return state.SnapshotPrimeSnapshot{state.SnapshotPrimeWaiting: state.SnapshotPrimeCohort{Pending: 1, OldestAt: now}}, nil
	})
	if err := m.refresh(t.Context(), r, ""); err != nil {
		t.Fatal(err)
	}
	if got := primeGauges(t, reg)["schedd_snapshot_prime_collection_last_success_timestamp_seconds"]; got != primeUnix(start) {
		t.Fatal("completion renewed read age", got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	r = primeObservationReaderFunc(func(context.Context, string) (state.SnapshotPrimeSnapshot, error) {
		cancel()
		return state.SnapshotPrimeSnapshot{}, nil
	})
	if err := m.refresh(ctx, r, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("late canceled success=%v", err)
	}
	if _, exists := primeGauges(t, reg)["schedd_snapshot_prime_pending/waiting"]; exists {
		t.Fatal("canceled read fabricated zero")
	}
}

func TestSnapshotPrimeMetricsScrapesDoNotReadOrBlockOnDatabase(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newSnapshotPrimeMetrics(reg)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m.start(ctx)
	entered, done := make(chan struct{}), make(chan struct{})
	r := primeObservationReaderFunc(func(ctx context.Context, node string) (state.SnapshotPrimeSnapshot, error) {
		close(entered)
		<-ctx.Done()
		return state.SnapshotPrimeSnapshot{}, ctx.Err()
	})
	go func() { m.runWithTicks(ctx, r, "node-a", nil, make(chan time.Time)); m.stop(); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("initial observation did not start")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Go(func() { _, err := reg.Gather(); errs <- err })
	}
	completed := make(chan struct{})
	go func() { wg.Wait(); close(completed) }()
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("scrapes blocked on database")
	}
	for range 20 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("observer did not stop")
	}
	if got := primeGauges(t, reg)["schedd_snapshot_prime_observation_enabled"]; got != 0 {
		t.Fatal("shutdown expectation remained enabled", got)
	}
}

func TestSnapshotPrimeMetricsSlowScrapeDoesNotLockRefresh(t *testing.T) {
	m := newSnapshotPrimeMetrics(prometheus.NewPedanticRegistry())
	metrics := make(chan prometheus.Metric)
	done := make(chan struct{})
	go func() { m.Collect(metrics); close(metrics); close(done) }()
	// Receiving the first sample leaves the collector blocked on its next send.
	<-metrics
	refreshed := make(chan error, 1)
	go func() {
		refreshed <- m.refresh(t.Context(), primeObservationReaderFunc(func(context.Context, string) (state.SnapshotPrimeSnapshot, error) {
			return state.SnapshotPrimeSnapshot{}, nil
		}), "")
	}()
	select {
	case err := <-refreshed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow scrape held observation lock")
	}
	for range metrics {
	}
	<-done
}

func TestSnapshotPrimeMetricsWiring(t *testing.T) {
	h := (&Loop{}).WithOpsMetrics(&wire.OpsMetrics{})
	if h.primeMetrics != nil {
		t.Fatal("zero sink registered collector")
	}
	ops := wire.NewOpsMetrics("schedd")
	h.WithOpsMetrics(ops)
	first := h.primeMetrics
	h.WithOpsMetrics(ops)
	if first == nil || h.primeMetrics != first {
		t.Fatal("repeat wiring replaced collector")
	}
	h.WithOpsMetrics(nil)
	if _, exists := primeGauges(t, ops.Registry())["schedd_snapshot_prime_collection_success"]; exists {
		t.Fatal("detached collector still registered")
	}
	h.WithOpsMetrics(ops)
	if h.primeMetrics == nil {
		t.Fatal("reattach failed")
	}
}

type primeListErrorStore struct {
	MemStore *state.MemStore
	err      error
	cancel   context.CancelFunc
}

func (s *primeListErrorStore) ListDeploymentsForOperator(context.Context, state.OperatorDeploymentFilter) ([]state.Deployment, error) {
	if s.cancel != nil {
		s.cancel()
	}
	return nil, s.err
}

func TestSnapshotPrimeRecoveryCountsActualQueriesAndDecisions(t *testing.T) {
	for _, name := range []string{"error", "canceled"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			err := errors.New("list failed")
			if name == "canceled" {
				err = context.Canceled
			}
			fake := &primeListErrorStore{MemStore: state.NewMemStore(), err: err}
			if name == "canceled" {
				fake.cancel = cancel
			}
			// Live parent with a matching cancellation returned by the list is an
			// error; only cancellation of the actual caller is classified canceled.
			loop := NewLoop(nil, &Engine{store: statetest.NewMem(fake)}, quietLog()).WithOpsMetrics(wire.NewOpsMetrics("schedd"))
			loop.runPrimeRecovery(ctx)
			if got := testutil.ToFloat64(loop.primeMetrics.queries.WithLabelValues(name)); got != 1 {
				t.Fatal("list outcome", got)
			}
		})
	}
	store, _, dep, loop := primeRecoveryFixture(t)
	loop.WithOpsMetrics(wire.NewOpsMetrics("schedd"))
	loop.runPrimeRecovery(t.Context())
	loop.waitPrimes()
	if got := testutil.ToFloat64(loop.primeMetrics.decisions.WithLabelValues("dispatch_requested")); got != 1 {
		t.Fatal("dispatch request", got)
	}
	loop.runPrimeRecovery(t.Context())
	loop.waitPrimes()
	if got := testutil.ToFloat64(loop.primeMetrics.decisions.WithLabelValues("publication_pending")); got != 1 {
		t.Fatal("parked capture", got)
	}
	if _, err := store.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10.0", StorageKey: "snap/counted", Tier: state.SnapshotTierInit}); err != nil {
		t.Fatal(err)
	}
	loop.runPrimeRecovery(t.Context())
	loop.waitPrimes()
	if got := testutil.ToFloat64(loop.primeMetrics.decisions.WithLabelValues("snapshot_recorded")); got != 1 {
		t.Fatal("snapshot record", got)
	}
	if _, err := store.AppendDeploymentStage(t.Context(), dep.ID, state.StageSnapshotPrepare, state.StageReadiness, time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	if err := loop.recoverPrimeCandidate(t.Context(), dep.ID, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(loop.primeMetrics.decisions.WithLabelValues("ineligible")); got != 1 {
		t.Fatal("later stage is not ineligible", got)
	}
	if got := testutil.ToFloat64(loop.primeMetrics.decisions.WithLabelValues("grace")); got != 0 {
		t.Fatal("later stage masquerades as young preparation", got)
	}
	if err := loop.recoverPrimeCandidate(t.Context(), "missing", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(loop.primeMetrics.decisions.WithLabelValues("not_found")); got != 1 {
		t.Fatal("missing candidate", got)
	}
}

func TestSnapshotPrimeObserverIndependentOfWorkPoolAndStop(t *testing.T) {
	store, app, _, loop := primeRecoveryFixture(t)
	ops := wire.NewOpsMetrics("schedd")
	loop.WithOpsMetrics(ops)
	release := fillSlots(t, loop.workPool(), workPrimeRecovery)
	defer release()
	stop := loop.startSnapshotPrimeObservation(t.Context())
	defer stop()
	deadline := time.After(3 * time.Second)
	for {
		got := primeGauges(t, ops.Registry())
		if n, present := got["schedd_snapshot_prime_pending/waiting"]; present {
			if n != 1 {
				t.Fatal(got)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("work pool blocked observer")
		case <-time.After(time.Millisecond):
		}
	}
	stop()
	got := primeGauges(t, ops.Registry())
	if got["schedd_snapshot_prime_observation_enabled"] != 0 || got["schedd_snapshot_prime_pending/waiting"] != 1 {
		t.Fatal("stop fabricated drain", got)
	}
	if rows, err := store.ListInstancesForApp(t.Context(), app.ID); err != nil || len(rows) != 0 {
		t.Fatal("observer created work")
	}
}
