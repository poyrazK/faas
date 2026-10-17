package imaged

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus"
)

type preparationProgressReaderFunc func(context.Context, string) (state.ImagePreparationProgressSnapshot, error)

func (f preparationProgressReaderFunc) ImagePreparationProgressSnapshot(ctx context.Context, node string) (state.ImagePreparationProgressSnapshot, error) {
	return f(ctx, node)
}

func preparationGauges(t *testing.T, reg *prometheus.Registry) map[string]float64 {
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
				if l.GetName() == "phase" {
					key += "/" + l.GetValue()
				}
			}
			out[key] = m.Gauge.GetValue()
		}
	}
	return out
}

func TestImagePreparationProgressMetricsFreshnessAndUnknownHistory(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newImagePreparationProgressMetrics(reg)
	now := time.Unix(1700000000, 0).UTC()
	m.now = func() time.Time { return now }
	const count = "imaged_image_preparation_pending/preparing"
	const oldest = "imaged_image_preparation_oldest_phase_started_timestamp_seconds/preparing"
	const unknown = "imaged_image_preparation_phase_age_unknown/preparing"
	const success = "imaged_image_preparation_collection_success"
	const last = "imaged_image_preparation_collection_last_success_timestamp_seconds"
	if _, exists := preparationGauges(t, reg)[count]; exists {
		t.Fatal("startup fabricated empty snapshot")
	}
	s := state.ImagePreparationProgressSnapshot{
		Preparing:    state.ImagePreparationPhaseSnapshot{Pending: 3, UnknownAge: 1, OldestPhaseStartedAt: now.Add(-45 * time.Minute)},
		ScanComplete: state.ImagePreparationPhaseSnapshot{Pending: 1, UnknownAge: 1},
	}
	reader := preparationProgressReaderFunc(func(ctx context.Context, node string) (state.ImagePreparationProgressSnapshot, error) {
		if node != "node-a" {
			t.Errorf("node=%q", node)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > api.ObsImagePreparationCollectionBudget {
			t.Error("unbounded observation read")
		}
		return s, nil
	})
	if err := m.refresh(t.Context(), reader, "node-a"); err != nil {
		t.Fatal(err)
	}
	got := preparationGauges(t, reg)
	if len(got) != 12 || got[count] != 3 || got[unknown] != 1 || got[oldest] != handoffUnix(s.Preparing.OldestPhaseStartedAt) || got["imaged_image_preparation_pending/scan_complete"] != 1 || got["imaged_image_preparation_oldest_phase_started_timestamp_seconds/scan_complete"] != 0 || got[success] != 1 {
		t.Fatal(got)
	}
	observed := got[last]
	now = now.Add(api.ObsImagePreparationFreshnessMaxAge)
	if _, exists := preparationGauges(t, reg)[count]; !exists {
		t.Fatal("exact freshness ceiling expired")
	}
	now = now.Add(time.Nanosecond)
	if _, exists := preparationGauges(t, reg)[count]; exists {
		t.Fatal("expired data remained visible")
	}
	broken := preparationProgressReaderFunc(func(context.Context, string) (state.ImagePreparationProgressSnapshot, error) {
		return state.ImagePreparationProgressSnapshot{}, errors.New("read failed")
	})
	if err := m.refresh(t.Context(), broken, "node-a"); err == nil {
		t.Fatal("failure hidden")
	}
	got = preparationGauges(t, reg)
	if got[success] != 0 || got[last] != observed {
		t.Fatal("failure renewed success", got)
	}
	if _, exists := got[count]; exists {
		t.Fatal("failure fabricated an empty or cached snapshot")
	}
	s = state.ImagePreparationProgressSnapshot{}
	if err := m.refresh(t.Context(), reader, "node-a"); err != nil {
		t.Fatal(err)
	}
	got = preparationGauges(t, reg)
	if value, exists := got[count]; !exists || value != 0 || got[oldest] != 0 || got[unknown] != 0 {
		t.Fatal("known empty lost", got)
	}
	now = now.Add(-time.Second)
	if _, exists := preparationGauges(t, reg)[count]; exists {
		t.Fatal("future observation accepted")
	}
}

func TestImagePreparationProgressMetricsRejectInconsistentEvidence(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	for name, p := range map[string]state.ImagePreparationPhaseSnapshot{
		"negative count":             {Pending: -1},
		"negative unknown":           {Pending: 1, UnknownAge: -1},
		"unknown exceeds count":      {Pending: 1, UnknownAge: 2},
		"missing known time":         {Pending: 1},
		"timestamp for empty":        {OldestPhaseStartedAt: now},
		"timestamp for unknown only": {Pending: 1, UnknownAge: 1, OldestPhaseStartedAt: now},
		"future phase":               {Pending: 1, OldestPhaseStartedAt: now.Add(time.Second)},
		"pre epoch":                  {Pending: 1, OldestPhaseStartedAt: time.Unix(-1, 0)},
	} {
		t.Run(name, func(t *testing.T) {
			reg := prometheus.NewPedanticRegistry()
			m := newImagePreparationProgressMetrics(reg)
			m.now = func() time.Time { return now }
			r := preparationProgressReaderFunc(func(context.Context, string) (state.ImagePreparationProgressSnapshot, error) {
				return state.ImagePreparationProgressSnapshot{LayerPublished: p}, nil
			})
			if err := m.refresh(t.Context(), r, ""); err == nil {
				t.Fatal("accepted inconsistent phase")
			}
			if _, exists := preparationGauges(t, reg)["imaged_image_preparation_pending/preparing"]; exists {
				t.Fatal("invalid snapshot partially emitted")
			}
		})
	}
}

func TestImagePreparationProgressMetricsReadStartAndCanceledRead(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newImagePreparationProgressMetrics(reg)
	now := time.Unix(1700000000, 0).UTC()
	start := now
	m.now = func() time.Time { return now }
	r := preparationProgressReaderFunc(func(context.Context, string) (state.ImagePreparationProgressSnapshot, error) {
		now = now.Add(time.Second)
		return state.ImagePreparationProgressSnapshot{Preparing: state.ImagePreparationPhaseSnapshot{Pending: 1, OldestPhaseStartedAt: now}}, nil
	})
	if err := m.refresh(t.Context(), r, ""); err != nil {
		t.Fatal(err)
	}
	if got := preparationGauges(t, reg)["imaged_image_preparation_collection_last_success_timestamp_seconds"]; got != handoffUnix(start) {
		t.Fatal("completion renewed read age", got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	r = preparationProgressReaderFunc(func(context.Context, string) (state.ImagePreparationProgressSnapshot, error) {
		cancel()
		return state.ImagePreparationProgressSnapshot{}, nil
	})
	if err := m.refresh(ctx, r, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("late canceled success=%v", err)
	}
	if _, exists := preparationGauges(t, reg)["imaged_image_preparation_pending/preparing"]; exists {
		t.Fatal("canceled read fabricated zero")
	}
}

func TestImagePreparationProgressMetricsScrapesDoNotReadOrBlockOnDatabase(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newImagePreparationProgressMetrics(reg)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m.start(ctx)
	entered, done := make(chan struct{}), make(chan struct{})
	r := preparationProgressReaderFunc(func(ctx context.Context, node string) (state.ImagePreparationProgressSnapshot, error) {
		close(entered)
		<-ctx.Done()
		return state.ImagePreparationProgressSnapshot{}, ctx.Err()
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
	if got := preparationGauges(t, reg)["imaged_image_preparation_observation_enabled"]; got != 0 {
		t.Fatal("shutdown expectation remained enabled", got)
	}
}

func TestImagePreparationProgressMetricsSlowScrapeDoesNotLockRefresh(t *testing.T) {
	m := newImagePreparationProgressMetrics(prometheus.NewPedanticRegistry())
	metrics := make(chan prometheus.Metric)
	done := make(chan struct{})
	go func() { m.Collect(metrics); close(metrics); close(done) }()
	// Receiving the first sample leaves the collector blocked on its next send.
	<-metrics
	refreshed := make(chan error, 1)
	go func() {
		refreshed <- m.refresh(t.Context(), preparationProgressReaderFunc(func(context.Context, string) (state.ImagePreparationProgressSnapshot, error) {
			return state.ImagePreparationProgressSnapshot{}, nil
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

func TestImagePreparationProgressMetricsWiring(t *testing.T) {
	h := (&Handler{}).WithOpsMetrics(&wire.OpsMetrics{})
	if h.preparationMetrics != nil {
		t.Fatal("zero sink registered collector")
	}
	ops := wire.NewOpsMetrics("imaged")
	h.WithOpsMetrics(ops)
	first := h.preparationMetrics
	h.WithOpsMetrics(ops)
	if first == nil || h.preparationMetrics != first {
		t.Fatal("repeat wiring replaced collector")
	}
	h.WithOpsMetrics(nil)
	if _, exists := preparationGauges(t, ops.Registry())["imaged_image_preparation_collection_success"]; exists {
		t.Fatal("detached collector still registered")
	}
	h.WithOpsMetrics(ops)
	if h.preparationMetrics == nil {
		t.Fatal("reattach failed")
	}
}
