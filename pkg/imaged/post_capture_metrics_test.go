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

type postCaptureReaderFunc func(context.Context) (state.PostCaptureSnapshot, error)

func (f postCaptureReaderFunc) PostCaptureSnapshot(ctx context.Context) (state.PostCaptureSnapshot, error) {
	return f(ctx)
}

func postCaptureGauges(t *testing.T, reg *prometheus.Registry) map[string]float64 {
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

func TestPostCaptureMetricsFreshnessAndUnknownHistory(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newPostCaptureMetrics(reg)
	now := time.Unix(1700000000, 0).UTC()
	m.now = func() time.Time { return now }
	const count = "imaged_post_capture_pending/publication_pending"
	const oldest = "imaged_post_capture_oldest_boundary_timestamp_seconds/publication_pending"
	const unknown = "imaged_post_capture_age_unknown/publication_pending"
	const success = "imaged_post_capture_collection_success"
	const last = "imaged_post_capture_collection_last_success_timestamp_seconds"
	if _, exists := postCaptureGauges(t, reg)[count]; exists {
		t.Fatal("startup fabricated empty snapshot")
	}
	s := state.PostCaptureSnapshot{
		state.PostCapturePublicationPending: state.SnapshotPrimeCohort{Pending: 3, UnknownAge: 1, OldestAt: now.Add(-45 * time.Minute)},
		state.PostCaptureUnknown:            state.SnapshotPrimeCohort{Pending: 1, UnknownAge: 1},
	}
	reader := postCaptureReaderFunc(func(ctx context.Context) (state.PostCaptureSnapshot, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > api.ObsPostCaptureCollectionBudget {
			t.Error("unbounded observation read")
		}
		return s, nil
	})
	if err := m.refresh(t.Context(), reader); err != nil {
		t.Fatal(err)
	}
	got := postCaptureGauges(t, reg)
	if len(got) != 27 || got[count] != 3 || got[unknown] != 1 || got[oldest] != handoffUnix(s[state.PostCapturePublicationPending].OldestAt) || got["imaged_post_capture_pending/unknown"] != 1 || got["imaged_post_capture_oldest_boundary_timestamp_seconds/unknown"] != 0 || got[success] != 1 {
		t.Fatal(got)
	}
	observed := got[last]
	now = now.Add(api.ObsPostCaptureFreshnessMaxAge)
	if _, exists := postCaptureGauges(t, reg)[count]; !exists {
		t.Fatal("exact freshness ceiling expired")
	}
	now = now.Add(time.Nanosecond)
	if _, exists := postCaptureGauges(t, reg)[count]; exists {
		t.Fatal("expired data remained visible")
	}
	broken := postCaptureReaderFunc(func(context.Context) (state.PostCaptureSnapshot, error) {
		return state.PostCaptureSnapshot{}, errors.New("read failed")
	})
	if err := m.refresh(t.Context(), broken); err == nil {
		t.Fatal("failure hidden")
	}
	got = postCaptureGauges(t, reg)
	if got[success] != 0 || got[last] != observed {
		t.Fatal("failure renewed success", got)
	}
	if _, exists := got[count]; exists {
		t.Fatal("failure fabricated an empty or cached snapshot")
	}
	s = state.PostCaptureSnapshot{}
	if err := m.refresh(t.Context(), reader); err != nil {
		t.Fatal(err)
	}
	got = postCaptureGauges(t, reg)
	if value, exists := got[count]; !exists || value != 0 || got[oldest] != 0 || got[unknown] != 0 {
		t.Fatal("known empty lost", got)
	}
	now = now.Add(-time.Second)
	if _, exists := postCaptureGauges(t, reg)[count]; exists {
		t.Fatal("future observation accepted")
	}
}

func TestPostCaptureMetricsRejectInconsistentEvidence(t *testing.T) {
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
			m := newPostCaptureMetrics(reg)
			m.now = func() time.Time { return now }
			r := postCaptureReaderFunc(func(context.Context) (state.PostCaptureSnapshot, error) {
				return state.PostCaptureSnapshot{state.PostCaptureActivationPending: p}, nil
			})
			if err := m.refresh(t.Context(), r); err == nil {
				t.Fatal("accepted inconsistent phase")
			}
			if _, exists := postCaptureGauges(t, reg)["imaged_post_capture_pending/publication_pending"]; exists {
				t.Fatal("invalid snapshot partially emitted")
			}
		})
	}
}

func TestPostCaptureMetricsReadStartAndCanceledRead(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newPostCaptureMetrics(reg)
	now := time.Unix(1700000000, 0).UTC()
	start := now
	m.now = func() time.Time { return now }
	r := postCaptureReaderFunc(func(context.Context) (state.PostCaptureSnapshot, error) {
		now = now.Add(time.Second)
		return state.PostCaptureSnapshot{state.PostCapturePublicationPending: state.SnapshotPrimeCohort{Pending: 1, OldestAt: now}}, nil
	})
	if err := m.refresh(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if got := postCaptureGauges(t, reg)["imaged_post_capture_collection_last_success_timestamp_seconds"]; got != handoffUnix(start) {
		t.Fatal("completion renewed read age", got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	r = postCaptureReaderFunc(func(context.Context) (state.PostCaptureSnapshot, error) {
		cancel()
		return state.PostCaptureSnapshot{}, nil
	})
	if err := m.refresh(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatalf("late canceled success=%v", err)
	}
	if _, exists := postCaptureGauges(t, reg)["imaged_post_capture_pending/publication_pending"]; exists {
		t.Fatal("canceled read fabricated zero")
	}
}

func TestPostCaptureMetricsScrapesDoNotReadOrBlockOnDatabase(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newPostCaptureMetrics(reg)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m.start(ctx)
	entered, done := make(chan struct{}), make(chan struct{})
	r := postCaptureReaderFunc(func(ctx context.Context) (state.PostCaptureSnapshot, error) {
		close(entered)
		<-ctx.Done()
		return state.PostCaptureSnapshot{}, ctx.Err()
	})
	go func() { m.runWithTicks(ctx, r, nil, make(chan time.Time)); m.stop(); close(done) }()
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
	if got := postCaptureGauges(t, reg)["imaged_post_capture_observation_enabled"]; got != 0 {
		t.Fatal("shutdown expectation remained enabled", got)
	}
}

func TestPostCaptureMetricsSlowScrapeDoesNotLockRefresh(t *testing.T) {
	m := newPostCaptureMetrics(prometheus.NewPedanticRegistry())
	metrics := make(chan prometheus.Metric)
	done := make(chan struct{})
	go func() { m.Collect(metrics); close(metrics); close(done) }()
	// Receiving the first sample leaves the collector blocked on its next send.
	<-metrics
	refreshed := make(chan error, 1)
	go func() {
		refreshed <- m.refresh(t.Context(), postCaptureReaderFunc(func(context.Context) (state.PostCaptureSnapshot, error) {
			return state.PostCaptureSnapshot{}, nil
		}))
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

func TestPostCaptureMetricsWiring(t *testing.T) {
	h := (&Handler{}).WithOpsMetrics(&wire.OpsMetrics{})
	if h.postCaptureMetrics != nil {
		t.Fatal("zero sink registered collector")
	}
	ops := wire.NewOpsMetrics("imaged")
	h.WithOpsMetrics(ops)
	first := h.postCaptureMetrics
	h.WithOpsMetrics(ops)
	if first == nil || h.postCaptureMetrics != first {
		t.Fatal("repeat wiring replaced collector")
	}
	h.WithOpsMetrics(nil)
	if _, exists := postCaptureGauges(t, ops.Registry())["imaged_post_capture_collection_success"]; exists {
		t.Fatal("detached collector still registered")
	}
	h.WithOpsMetrics(ops)
	if h.postCaptureMetrics == nil {
		t.Fatal("reattach failed")
	}
}

func TestPostCaptureLoopObservesWhilePreparationBlocked(t *testing.T) {
	store, app, dep := imagePreparationFixture(t, "memory")
	_, candidate := postCaptureCandidate(t, store, app.AccountID, "post-pending")
	published := postCapturePublish(t, store, candidate, state.SnapshotTierInit)
	if _, err := store.(state.DeploymentImagePreparationStore).BeginImagePreparation(t.Context(), dep.ID, "node-a"); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	builder := &fakeBuilder{bytesOut: 9, buildHook: func() { close(entered); <-release }}
	scans := 0
	ops := wire.NewOpsMetrics("imaged")
	h := imagePreparationHandler(t, store, builder, &fakeNotifier{}, &scans).WithOpsMetrics(ops)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	t.Cleanup(func() {
		cancel()
		once.Do(func() { close(release) })
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("loop did not stop")
		}
	})
	go func() {
		done <- NewLoop(LoopConfig{Handler: h, Store: store, LvUsedPct: func(context.Context) (float64, error) { return 0, nil }}).Run(ctx)
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("preparation did not start")
	}
	deadline := time.After(3 * time.Second)
	for {
		got := postCaptureGauges(t, ops.Registry())
		if n, present := got["imaged_post_capture_pending/activation_pending"]; present {
			if n != 1 || got["imaged_post_capture_oldest_boundary_timestamp_seconds/activation_pending"] != handoffUnix(published.CreatedAt) {
				t.Fatal(got)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("blocked preparation froze post-capture observer")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	got := postCaptureGauges(t, ops.Registry())
	if got["imaged_post_capture_observation_enabled"] != 0 || got["imaged_post_capture_pending/activation_pending"] != 1 {
		t.Fatal("shutdown fabricated drain", got)
	}
}
