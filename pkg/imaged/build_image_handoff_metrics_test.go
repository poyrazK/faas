package imaged

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildexport"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type handoffReaderFunc func(context.Context, string) (state.BuildImageHandoffSnapshot, error)

func (f handoffReaderFunc) BuildImageHandoffSnapshot(ctx context.Context, node string) (state.BuildImageHandoffSnapshot, error) {
	return f(ctx, node)
}

func handoffGauges(t *testing.T, reg *prometheus.Registry) map[string]float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, family := range families {
		if len(family.Metric) == 1 && family.Metric[0].Gauge != nil {
			out[family.GetName()] = family.Metric[0].Gauge.GetValue()
		}
	}
	return out
}

func TestBuildImageHandoffMetricsFreshnessAndFailures(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newBuildImageHandoffMetrics(reg)
	now := time.Unix(1700000000, 0).UTC()
	m.now = func() time.Time { return now }
	good := handoffReaderFunc(func(ctx context.Context, node string) (state.BuildImageHandoffSnapshot, error) {
		if node != "node-a" {
			t.Errorf("reader node=%q", node)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > api.ObsBuildImageHandoffCollectionBudget {
			t.Error("unbounded read")
		}
		return state.BuildImageHandoffSnapshot{Pending: 2, OldestFinishedAt: now.Add(-10 * time.Minute)}, nil
	})
	if _, present := handoffGauges(t, reg)["imaged_build_handoff_pending"]; present {
		t.Fatal("startup fabricated empty backlog")
	}
	if err := m.refresh(t.Context(), good, "node-a"); err != nil {
		t.Fatal(err)
	}
	initial := handoffGauges(t, reg)
	if initial["imaged_build_handoff_pending"] != 2 || initial["imaged_build_handoff_oldest_finished_timestamp_seconds"] != float64(now.Add(-10*time.Minute).Unix()) {
		t.Fatal(initial)
	}
	now = now.Add(api.ObsBuildImageHandoffFreshnessMaxAge)
	if _, present := handoffGauges(t, reg)["imaged_build_handoff_pending"]; !present {
		t.Fatal("exact freshness boundary prematurely expired")
	}
	now = now.Add(time.Nanosecond)
	if _, present := handoffGauges(t, reg)["imaged_build_handoff_pending"]; present {
		t.Fatal("expired backlog still emitted")
	}
	bad := handoffReaderFunc(func(context.Context, string) (state.BuildImageHandoffSnapshot, error) {
		return state.BuildImageHandoffSnapshot{}, errors.New("database unavailable")
	})
	if err := m.refresh(t.Context(), bad, "node-a"); err == nil {
		t.Fatal("failed read accepted")
	}
	failed := handoffGauges(t, reg)
	if failed["imaged_build_handoff_collection_success"] != 0 || failed["imaged_build_handoff_collection_last_success_timestamp_seconds"] != initial["imaged_build_handoff_collection_last_success_timestamp_seconds"] {
		t.Fatal(failed)
	}
	if _, present := failed["imaged_build_handoff_pending"]; present {
		t.Fatal("failed read exposed old backlog")
	}
	if err := m.refresh(t.Context(), handoffReaderFunc(func(context.Context, string) (state.BuildImageHandoffSnapshot, error) {
		return state.BuildImageHandoffSnapshot{}, nil
	}), "node-a"); err != nil {
		t.Fatal(err)
	}
	if got, present := handoffGauges(t, reg)["imaged_build_handoff_pending"]; !present || got != 0 {
		t.Fatal("successful empty read is not known zero")
	}
}

func TestBuildImageHandoffMetricsBootstrapCompatibility(t *testing.T) {
	h := &Handler{}
	h.WithOpsMetrics(nil).WithOpsMetrics(&wire.OpsMetrics{})
	if h.handoffMetrics != nil {
		t.Fatal("zero-value sink registered a collector")
	}
	ops := wire.NewOpsMetrics("imaged")
	h.WithOpsMetrics(ops)
	first := h.handoffMetrics
	h.WithOpsMetrics(ops)
	if first == nil || h.handoffMetrics != first {
		t.Fatal("repeat wiring replaced metrics")
	}
	h.WithOpsMetrics(nil)
	if _, ok := handoffGauges(t, ops.Registry())["imaged_build_handoff_collection_success"]; ok {
		t.Fatal("disabled collector remained registered")
	}
	h.WithOpsMetrics(ops)
	if h.handoffMetrics == nil {
		t.Fatal("reattaching sink failed")
	}
}

func TestBuildImageHandoffMetricsRejectMalformedEvidence(t *testing.T) {
	now := time.Unix(1700000000, 0)
	for _, s := range []state.BuildImageHandoffSnapshot{{Pending: -1}, {Pending: 1}, {OldestFinishedAt: now}, {Pending: 1, OldestFinishedAt: now.Add(time.Second)}} {
		reg := prometheus.NewPedanticRegistry()
		m := newBuildImageHandoffMetrics(reg)
		m.now = func() time.Time { return now }
		err := m.refresh(t.Context(), handoffReaderFunc(func(context.Context, string) (state.BuildImageHandoffSnapshot, error) { return s, nil }), "node-a")
		if err == nil {
			t.Fatalf("accepted %+v", s)
		}
		if _, ok := handoffGauges(t, reg)["imaged_build_handoff_pending"]; ok {
			t.Fatal("invalid evidence emitted")
		}
	}
}

func TestBuildImageHandoffMetricsScrapesAreIndependentAndBounded(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newBuildImageHandoffMetrics(reg)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, done := make(chan struct{}), make(chan struct{})
	reader := handoffReaderFunc(func(ctx context.Context, node string) (state.BuildImageHandoffSnapshot, error) {
		close(entered)
		<-ctx.Done()
		return state.BuildImageHandoffSnapshot{}, ctx.Err()
	})
	go func() { m.runWithTicks(ctx, reader, "node-a", nil, make(chan time.Time)); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("initial read was not immediate")
	}
	scraped := make(chan error, 20)
	for range 20 {
		go func() { _, err := reg.Gather(); scraped <- err }()
	}
	for range 20 {
		select {
		case err := <-scraped:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("scrape blocked on database read")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("collector failed to stop")
	}
	if _, ok := handoffGauges(t, reg)["imaged_build_handoff_pending"]; ok {
		t.Fatal("canceled read became empty success")
	}
}

type handoffQueryStore struct {
	MemStore                   *state.MemStore
	resumeErr, awaitingErr     error
	resumeCalls, awaitingCalls int
	publicationErr             error
}

func (s *handoffQueryStore) ListResumableImagePreparations(ctx context.Context, node string, limit int) ([]state.BuildImageWork, error) {
	s.resumeCalls++
	if s.resumeErr != nil {
		return nil, s.resumeErr
	}
	return s.MemStore.ListResumableImagePreparations(ctx, node, limit)
}
func (s *handoffQueryStore) ListBuildsAwaitingImage(ctx context.Context, node string, limit int) ([]state.BuildImageWork, error) {
	s.awaitingCalls++
	if s.awaitingErr != nil {
		return nil, s.awaitingErr
	}
	return s.MemStore.ListBuildsAwaitingImage(ctx, node, limit)
}
func (s *handoffQueryStore) PublishImagePreparationLayer(ctx context.Context, id, token, path, key string, bytes int64) error {
	if s.publicationErr != nil {
		return s.publicationErr
	}
	return s.MemStore.PublishImagePreparationLayer(ctx, id, token, path, key, bytes)
}

func TestBuildImageHandoffRecoveryQueryOutcomes(t *testing.T) {
	for _, outcome := range []string{"success", "error", "canceled"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fake := &handoffQueryStore{MemStore: state.NewMemStore()}
			if outcome == "error" {
				fake.resumeErr = errors.New("resume database failure")
				fake.awaitingErr = errors.New("awaiting database failure")
			}
			if outcome == "canceled" {
				cancel()
				fake.resumeErr = ctx.Err()
				fake.awaitingErr = ctx.Err()
			}
			store := statetest.NewMem(fake)
			h := newHandler(store.MemStore)
			h.store = store
			h.WithOpsMetrics(wire.NewOpsMetrics("imaged"))
			NewLoop(LoopConfig{Handler: h, Store: store}).recoverBuildHandoffs(ctx)
			if fake.resumeCalls != 1 || fake.awaitingCalls != 1 {
				t.Fatalf("queries=%d/%d", fake.resumeCalls, fake.awaitingCalls)
			}
			for _, source := range []string{"resume", "awaiting"} {
				if got := testutil.ToFloat64(h.handoffMetrics.queries.WithLabelValues(source, outcome)); got != 1 {
					t.Fatalf("%s/%s=%v", source, outcome, got)
				}
			}
			if h.handoffMetrics.inflight || h.handoffMetrics.completed.IsZero() {
				t.Fatal("completed pass progress missing")
			}
		})
	}
}

func TestBuildImageHandoffRecoveryReturnIsNotCommit(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newBuildImageHandoffMetrics(reg)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, tc := range []struct {
		err     error
		outcome string
		ctx     context.Context
	}{
		{nil, "returned", t.Context()}, {imageRecoveryError(errors.New("storage offline")), "retryable", t.Context()},
		{imageRecoveryError(buildexport.ErrBusy), "deferred", t.Context()}, {state.ErrConflict, "deferred", t.Context()},
		{db.ErrNotificationNotOwned, "not_owned", t.Context()}, {errors.New("assembly failure"), "error", t.Context()}, {ctx.Err(), "canceled", ctx},
	} {
		m.observeWork(tc.ctx, "resume", tc.err)
		if testutil.ToFloat64(m.work.WithLabelValues("resume", tc.outcome)) < 1 {
			t.Fatal(tc.outcome)
		}
	}
	if got := testutil.ToFloat64(m.commits.WithLabelValues("handed_off")); got != 0 {
		t.Fatalf("handler return fabricated %v commits", got)
	}
}

func TestBuildImageHandoffMetricsProgressAndShutdown(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newBuildImageHandoffMetrics(reg)
	now := time.Unix(1700000000, 0)
	m.now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m.start(ctx)
	m.beginRecovery()
	now = now.Add(45 * time.Minute)
	got := handoffGauges(t, reg)
	if got["imaged_build_handoff_recovery_inflight"] != 1 || got["imaged_build_handoff_recovery_last_completed_timestamp_seconds"] != 0 || got["imaged_build_handoff_recovery_last_attempt_timestamp_seconds"] != float64(now.Add(-45*time.Minute).Unix()) {
		t.Fatal(got)
	}
	cancel()
	got = handoffGauges(t, reg)
	if got["imaged_build_handoff_recovery_enabled"] != 0 || got["imaged_build_handoff_recovery_inflight"] != 1 {
		t.Fatal("shutdown fabricated drain", got)
	}
	m.finishRecovery()
	m.stop()
}

func TestBuildImageHandoffMetricsSnapshotDoesNotHoldLockWhileEmitting(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	m := newBuildImageHandoffMetrics(reg)
	samples, done := make(chan prometheus.Metric), make(chan struct{})
	go func() { m.Collect(samples); close(samples); close(done) }()
	<-samples
	var once sync.Once
	t.Cleanup(func() {
		once.Do(func() {
			for range samples {
			}
			<-done
		})
	})
	updated := make(chan struct{})
	go func() { m.beginRecovery(); m.finishRecovery(); close(updated) }()
	select {
	case <-updated:
	case <-time.After(time.Second):
		t.Fatal("collector held mutex across output")
	}
}

func TestBuildImageHandoffMetricsReadDeadlineIsEnforced(t *testing.T) {
	m := newBuildImageHandoffMetrics(prometheus.NewPedanticRegistry())
	started := time.Now()
	err := m.refresh(t.Context(), handoffReaderFunc(func(ctx context.Context, _ string) (state.BuildImageHandoffSnapshot, error) {
		<-ctx.Done()
		return state.BuildImageHandoffSnapshot{}, ctx.Err()
	}), "node-a")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 5*time.Second {
		t.Fatalf("bounded read: %v after %s", err, time.Since(started))
	}
}

func TestBuildImageHandoffFailedPublicationIsNotACommit(t *testing.T) {
	store, app, dep := imagePreparationFixture(t, "memory")
	fake := &handoffQueryStore{MemStore: store.(*state.MemStore), publicationErr: errors.New("checkpoint write failed")}
	builder, notif, scans := &fakeBuilder{bytesOut: 9}, &fakeNotifier{}, 0
	h := imagePreparationHandler(t, statetest.NewMem(fake), builder, notif, &scans).WithOpsMetrics(wire.NewOpsMetrics("imaged"))
	err := h.handleSnapshotBoot(t.Context(), snapshotBootPayload{AppID: app.ID, DeploymentID: dep.ID, NodeID: "node-a"})
	if !errors.Is(err, errImagePreparationRecovery) {
		t.Fatal(err)
	}
	for _, phase := range []string{"layer_published", "scan_complete", "handed_off"} {
		if testutil.ToFloat64(h.handoffMetrics.commits.WithLabelValues(phase)) != 0 {
			t.Fatalf("failed publication fabricated %s", phase)
		}
	}
	fake.publicationErr = nil
	NewLoop(LoopConfig{Handler: h, Store: h.store}).recoverBuildHandoffs(t.Context())
	for _, phase := range []string{"layer_published", "scan_complete", "handed_off"} {
		if got := testutil.ToFloat64(h.handoffMetrics.commits.WithLabelValues(phase)); got != 1 {
			t.Fatalf("recovery %s=%v", phase, got)
		}
	}
	if testutil.ToFloat64(h.handoffMetrics.work.WithLabelValues("resume", "returned")) != 1 {
		t.Fatal("recovery return missing")
	}
}

func TestBuildImageHandoffBusyArtifactDefersWithoutFalseCompletion(t *testing.T) {
	store, _, dep := imagePreparationFixture(t, "memory")
	artifact := filepath.Join(t.TempDir(), "export", "build", "out", "image.tar")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o750); err != nil {
		t.Fatal(err)
	}
	lease, err := os.OpenFile(artifact, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if _, err := lease.WriteString("archive"); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lease.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(t.Context(), dep.ID, artifact, "builder/input", 7); err != nil {
		t.Fatal(err)
	}
	scans := 0
	h := imagePreparationHandler(t, store, &fakeBuilder{bytesOut: 9}, &fakeNotifier{}, &scans).WithOpsMetrics(wire.NewOpsMetrics("imaged"))
	// Begin before recovery so this is checkpointed resumable work. It must
	// not be counted as an untouched successful-build handoff.
	if _, err := store.(state.DeploymentImagePreparationStore).BeginImagePreparation(t.Context(), dep.ID, "node-a"); err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(LoopConfig{Handler: h, Store: store})
	loop.recoverBuildHandoffs(t.Context())
	if got := testutil.ToFloat64(h.handoffMetrics.work.WithLabelValues("resume", "deferred")); got != 1 {
		t.Fatalf("busy lease outcome=%v", got)
	}
	if got := testutil.ToFloat64(h.handoffMetrics.commits.WithLabelValues("handed_off")); got != 0 {
		t.Fatalf("busy lease fabricated completion=%v", got)
	}
	current, err := store.DeploymentByID(t.Context(), dep.ID)
	if err != nil || current.Status.IsTerminal() {
		t.Fatalf("busy lease changed terminal state: %s %v", current.Status, err)
	}
	reader := store.(state.ImagePreparationProgressObservationStore)
	before, err := reader.ImagePreparationProgressSnapshot(t.Context(), "node-a")
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		loop.recoverBuildHandoffs(t.Context())
	}
	after, err := reader.ImagePreparationProgressSnapshot(t.Context(), "node-a")
	if err != nil || after != before || after.Preparing.Pending != 1 || after.Preparing.OldestPhaseStartedAt.IsZero() {
		t.Fatalf("busy retries renewed phase progress: before=%+v after=%+v err=%v", before, after, err)
	}
	if err := syscall.Flock(int(lease.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	loop.recoverBuildHandoffs(t.Context())
	if got := testutil.ToFloat64(h.handoffMetrics.commits.WithLabelValues("handed_off")); got != 1 {
		t.Fatalf("released lease did not recover: %v", got)
	}
}

func TestBuildImageHandoffLoopObservesWhilePreparationIsBlocked(t *testing.T) {
	store, app, dep := imagePreparationFixture(t, "memory")
	waitingApp, err := store.CreateApp(t.Context(), state.App{AccountID: app.AccountID, Slug: "waiting-handoff", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	_, build := seedObservedHandoff(t, store, waitingApp.ID, "node-a")
	if _, err := store.(state.DeploymentImagePreparationStore).BeginImagePreparation(t.Context(), dep.ID, "node-a"); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	builder := &fakeBuilder{bytesOut: 9, buildHook: func() { close(entered); <-release }}
	scans := 0
	ops := wire.NewOpsMetrics("imaged")
	h := imagePreparationHandler(t, store, builder, &fakeNotifier{}, &scans).WithOpsMetrics(ops)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	t.Cleanup(func() {
		cancel()
		releaseOnce.Do(func() { close(release) })
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
		t.Fatal("recovery did not enter image preparation")
	}
	deadline := time.After(3 * time.Second)
	for {
		got := handoffGauges(t, ops.Registry())
		if value, present := got["imaged_build_handoff_pending"]; present {
			if value != 1 || got["imaged_build_handoff_oldest_finished_timestamp_seconds"] != handoffUnix(build.FinishedAt) || got["imaged_build_handoff_recovery_inflight"] != 1 {
				t.Fatal(got)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("blocked preparation froze independent collector")
		case <-time.After(time.Millisecond):
		}
	}
	for {
		got := preparationGauges(t, ops.Registry())
		if count, present := got["imaged_image_preparation_pending/preparing"]; present {
			if count != 1 || got["imaged_image_preparation_oldest_phase_started_timestamp_seconds/preparing"] <= 0 {
				t.Fatal(got)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("blocked work froze preparation observer")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if got := preparationGauges(t, ops.Registry()); got["imaged_image_preparation_observation_enabled"] != 0 || got["imaged_image_preparation_pending/preparing"] != 1 {
		t.Fatal("shutdown fabricated preparation drain", got)
	}
	if got := handoffGauges(t, ops.Registry()); got["imaged_build_handoff_recovery_enabled"] != 0 || got["imaged_build_handoff_recovery_inflight"] != 1 {
		t.Fatal("cancellation fabricated drain", got)
	}
}
