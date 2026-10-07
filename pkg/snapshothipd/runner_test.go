package snapshothipd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type fakeReplicaStore struct {
	job    state.SnapshotReplicaJob
	queued int
	ready  bool
	failed error
}

type renewingReplicaStore struct {
	fakeReplicaStore
	renewErr   error
	renewCalls int
	renewed    chan struct{}
	renewOnce  sync.Once
}

func (f *renewingReplicaStore) RenewSnapshotReplicaLease(context.Context, string, string, string) error {
	f.renewCalls++
	if f.renewed != nil {
		f.renewOnce.Do(func() { close(f.renewed) })
	}
	return f.renewErr
}

func (f *renewingReplicaStore) MarkSnapshotReplicaReadyWithLease(context.Context, string, string, string) error {
	f.ready = true
	return nil
}

func (f *renewingReplicaStore) MarkSnapshotReplicaFailedWithLease(_ context.Context, _, _, _ string, err error) error {
	f.failed = err
	return nil
}

func (f *fakeReplicaStore) EnqueueSnapshotReplicasForNode(context.Context, string) (int, error) {
	f.queued++
	return f.queued, nil
}

func (f *fakeReplicaStore) ClaimSnapshotReplica(context.Context, string) (state.SnapshotReplicaJob, error) {
	if f.ready || f.failed != nil {
		return state.SnapshotReplicaJob{}, state.ErrNotFound
	}
	return f.job, nil
}

func (f *fakeReplicaStore) RenewSnapshotReplicaLease(context.Context, string, string, string) error {
	return nil
}

func (f *fakeReplicaStore) MarkSnapshotReplicaReadyWithLease(context.Context, string, string, string) error {
	f.ready = true
	return nil
}

func (f *fakeReplicaStore) MarkSnapshotReplicaFailedWithLease(_ context.Context, _, _, _ string, err error) error {
	f.failed = err
	return nil
}

func (f *fakeReplicaStore) ReadySnapshotReplicaNodes(context.Context, string) ([]string, error) {
	if f.ready {
		return []string{f.job.NodeID}, nil
	}
	return nil, nil
}

type fakeBackend struct {
	objects map[string][]byte
	gets    []string
	failKey string
}

type leaseGateBackend struct {
	started      chan struct{}
	release      chan struct{}
	canceled     chan struct{}
	startOnce    sync.Once
	canceledOnce sync.Once
}

func (f *leaseGateBackend) Put(context.Context, string, io.Reader) error { return nil }

func (f *leaseGateBackend) Get(ctx context.Context, _ string) (io.ReadCloser, error) {
	f.startOnce.Do(func() { close(f.started) })
	select {
	case <-f.release:
		return io.NopCloser(bytes.NewReader([]byte("artifact"))), nil
	case <-ctx.Done():
		f.canceledOnce.Do(func() { close(f.canceled) })
		return nil, ctx.Err()
	}
}

func (f *leaseGateBackend) Delete(context.Context, string) error { return nil }

func (f *fakeBackend) Put(context.Context, string, io.Reader) error { return nil }

func (f *fakeBackend) Get(_ context.Context, key string) (io.ReadCloser, error) {
	f.gets = append(f.gets, key)
	if key == f.failKey {
		return nil, io.ErrUnexpectedEOF
	}
	data, ok := f.objects[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *fakeBackend) Delete(context.Context, string) error { return nil }

type fakeLocalBackend struct {
	*fakeBackend
	local    map[string]bool
	resolved []string
}

func (f *fakeLocalBackend) LocalPath(key string) (string, bool, error) {
	f.resolved = append(f.resolved, key)
	if f.local[key] {
		return "/cache/" + key, true, nil
	}
	return "", false, nil
}

type fakeMetrics struct {
	outcomes  []string
	latencies []time.Duration
}

func (f *fakeMetrics) ObserveFanout(outcome, region string) {
	f.outcomes = append(f.outcomes, outcome+":"+region)
}

func (f *fakeMetrics) ObserveFanoutLatency(_ string, latency time.Duration) {
	f.latencies = append(f.latencies, latency)
}

func TestRunnerTickPrepositionsCompleteRestoreClosure(t *testing.T) {
	store := &fakeReplicaStore{job: state.SnapshotReplicaJob{
		SnapshotID: "snap-1", DeploymentID: "dep-1", NodeID: "node-2", Region: "europe-west3",
		StorageKey: "snap/dep-1/mem", VMStateStorageKey: "snap/dep-1/vmstate",
		LayerStorageKeys: []string{"apps/acme/dep-1.ext4", "apps/acme/dep-1-metrics.ext4"}, Attempts: 1,
		QueuedAt: time.Now().Add(-50 * time.Millisecond),
	}}
	backend := &fakeBackend{objects: map[string][]byte{
		"snap/dep-1/mem":               []byte("memory"),
		"snap/dep-1/vmstate":           []byte("vmstate"),
		"apps/acme/dep-1.ext4":         []byte("app"),
		"apps/acme/dep-1-metrics.ext4": []byte("sidecar"),
	}}
	metrics := &fakeMetrics{}
	r := New(store, backend, "node-2", slog.Default()).WithMetrics(metrics)
	r.runTick(context.Background())

	if !store.ready {
		t.Fatal("snapshot replica was not marked ready")
	}
	if got, want := len(backend.gets), 4; got != want {
		t.Fatalf("Get calls = %d, want %d (%v)", got, want, backend.gets)
	}
	if got, want := metrics.outcomes, []string{"ready:europe-west3"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("metrics = %v, want %v", got, want)
	}
	if len(metrics.latencies) != 1 || metrics.latencies[0] < 50*time.Millisecond {
		t.Fatalf("latencies = %v, want one queue-to-ready sample >= 50ms", metrics.latencies)
	}
}

func TestRunnerRevalidationDoesNotChangeInitialFanoutMetrics(t *testing.T) {
	store := &fakeReplicaStore{job: state.SnapshotReplicaJob{
		SnapshotID: "snap-revalidate", DeploymentID: "dep-revalidate", NodeID: "node-2", Region: "europe-west3",
		StorageKey: "snap/dep-revalidate/mem", VMStateStorageKey: "snap/dep-revalidate/vmstate",
		Attempts: 1, Revalidation: true, QueuedAt: time.Now(),
	}}
	backend := &fakeBackend{objects: map[string][]byte{
		"snap/dep-revalidate/mem":     []byte("memory"),
		"snap/dep-revalidate/vmstate": []byte("vmstate"),
	}}
	metrics := &fakeMetrics{}
	New(store, backend, "node-2", slog.Default()).WithMetrics(metrics).runTick(context.Background())

	if !store.ready {
		t.Fatal("revalidated snapshot replica was not marked ready")
	}
	if len(metrics.outcomes) != 0 || len(metrics.latencies) != 0 {
		t.Fatalf("revalidation changed initial fan-out metrics: outcomes=%v latencies=%v", metrics.outcomes, metrics.latencies)
	}
}

// Prod hunt #3: revalidation re-downloaded every replica the bounded cache had
// evicted, which evicted another, so idle compute nodes rewrote 1-2 GiB
// snapshot files every few seconds. An evicted replica must be retired
// without a parent read; a resident one stays ready without a fetch.
func TestRunnerRevalidationRetiresEvictedReplicaWithoutRefetching(t *testing.T) {
	const mem, vmstate = "snap/dep-evicted/mem", "snap/dep-evicted/vmstate"
	for _, tc := range []struct {
		name      string
		resident  []string
		wantReady bool
	}{
		{name: "evicted", resident: []string{mem}, wantReady: false},
		{name: "resident", resident: []string{mem, vmstate}, wantReady: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := &fakeBackend{objects: map[string][]byte{mem: []byte("memory"), vmstate: []byte("vmstate")}}
			cache, err := storage.NewLocalCacheBackend(parent, t.TempDir(), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range tc.resident {
				if err := cache.Put(context.Background(), key, bytes.NewReader(parent.objects[key])); err != nil {
					t.Fatal(err)
				}
			}
			store := &fakeReplicaStore{job: state.SnapshotReplicaJob{
				SnapshotID: "snap-evicted", DeploymentID: "dep-evicted", NodeID: "node-2",
				StorageKey: mem, VMStateStorageKey: vmstate, Attempts: 1, Revalidation: true,
			}}
			New(store, cache, "node-2", slog.Default()).runWorkTick(context.Background())
			if len(parent.gets) != 0 {
				t.Fatalf("revalidation read the parent: %v", parent.gets)
			}
			if store.ready != tc.wantReady {
				t.Fatalf("ready = %v, want %v (failed=%v)", store.ready, tc.wantReady, store.failed)
			}
			if !tc.wantReady && !errors.Is(store.failed, errReplicaEvicted) {
				t.Fatalf("failed = %v, want an eviction retirement", store.failed)
			}
		})
	}
}

func TestRunnerDefaultIntervalFitsPrepositionedWakeBudget(t *testing.T) {
	if DefaultInterval >= 200*time.Millisecond {
		t.Fatalf("DefaultInterval = %s, want < 200ms prepositioned-wake budget", DefaultInterval)
	}
	if got := New(nil, nil, "node", nil).Interval(); got != DefaultInterval {
		t.Fatalf("Interval() = %s, want default %s", got, DefaultInterval)
	}
}

func TestRunnerFanoutPopulatesReplicaCacheAndAdvertisesLocality(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	region := state.DefaultLocalityLabel
	second := state.ComputeNode{
		ID: "node-2", Name: "compute-2", TargetURL: "unix:///run/faas/compute-2.sock",
		AdmissionCeilingMB: 4096, VCPUBudget: 16, Active: true, Region: &region,
	}
	if _, err := store.CreateComputeNode(ctx, second); err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	account, err := store.CreateAccount(ctx, "fanout@example.com", "pro")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "fanout-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		ID: "fanout-deployment", AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:fanout", Status: state.DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
		t.Fatalf("UpdateDeploymentStatus: %v", err)
	}
	snapshot, err := store.CreateSnapshot(ctx, state.Snapshot{
		ID: "fanout-snapshot", DeploymentID: dep.ID, FCVersion: "fc-1", StorageKey: state.SnapMemKey(dep.ID),
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	origin, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("ComputeNodeByName: %v", err)
	}
	if err := store.RecordSnapshotOrigin(ctx, snapshot.ID, origin.ID); err != nil {
		t.Fatalf("RecordSnapshotOrigin: %v", err)
	}

	keys := []string{
		state.SnapMemKey(dep.ID),
		state.SnapVMStateKey(dep.ID),
		"layers/" + dep.ID + ".ext4",
	}
	parent := &fakeBackend{objects: map[string][]byte{
		keys[0]: []byte("memory"),
		keys[1]: []byte("vmstate"),
		keys[2]: []byte("rootfs"),
	}}
	cache, err := storage.NewLocalCacheBackend(parent, t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	r := New(store, cache, second.ID, slog.Default()).WithMaxPerTick(1)
	r.runTick(ctx)

	ready, err := store.ReadySnapshotReplicaNodes(ctx, snapshot.ID)
	if err != nil {
		t.Fatalf("ReadySnapshotReplicaNodes: %v", err)
	}
	if len(ready) != 1 || ready[0] != second.ID {
		t.Fatalf("ready nodes = %v, want [%s]", ready, second.ID)
	}
	locality, err := store.SnapshotLocalityFor(ctx, snapshot.ID)
	if err != nil {
		t.Fatalf("SnapshotLocalityFor: %v", err)
	}
	if locality.OriginNodeID != origin.ID || len(locality.ReadyNodeIDs) != 1 || locality.ReadyNodeIDs[0] != second.ID {
		t.Fatalf("snapshot locality = %+v, want origin=%s ready=[%s]", locality, origin.ID, second.ID)
	}
	if got, want := len(parent.gets), len(keys); got != want {
		t.Fatalf("parent Get calls = %d, want %d (%v)", got, want, parent.gets)
	}
	for _, key := range keys {
		if _, local, err := cache.LocalPath(key); err != nil || !local {
			t.Fatalf("cache LocalPath(%q) = local=%v err=%v, want local=true", key, local, err)
		}
	}
}

func TestRunnerTickFailureIsRetryable(t *testing.T) {
	store := &fakeReplicaStore{job: state.SnapshotReplicaJob{
		SnapshotID: "snap-1", DeploymentID: "dep-1", NodeID: "node-2", Region: "local",
		StorageKey: "snap/dep-1/mem", VMStateStorageKey: "snap/dep-1/vmstate",
	}}
	backend := &fakeBackend{objects: map[string][]byte{"snap/dep-1/mem": []byte("memory")}, failKey: "snap/dep-1/vmstate"}
	metrics := &fakeMetrics{}
	r := New(store, backend, "node-2", slog.Default()).WithMetrics(metrics)
	r.runTick(context.Background())

	if store.failed == nil {
		t.Fatal("failed replica was not recorded")
	}
	if store.ready {
		t.Fatal("failed replica was marked ready")
	}
	if got, want := metrics.outcomes, []string{"failed:local"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("metrics = %v, want %v", got, want)
	}
}

func TestRunnerRenewsLongSnapshotLeaseBeforeCompleting(t *testing.T) {
	store := &renewingReplicaStore{
		fakeReplicaStore: fakeReplicaStore{job: state.SnapshotReplicaJob{
			SnapshotID: "snap-renew", DeploymentID: "dep-renew", NodeID: "node-2", LeaseToken: "lease-1",
			StorageKey: "snap/dep-renew/mem", VMStateStorageKey: "snap/dep-renew/vmstate",
		}},
		renewed: make(chan struct{}),
	}
	backend := &leaseGateBackend{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
	r := New(store, backend, "node-2", slog.Default()).WithMaxPerTick(1).WithLeaseRenewInterval(time.Millisecond)
	done := make(chan struct{})
	go func() {
		r.runWorkTick(context.Background())
		close(done)
	}()
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("snapshot sync did not start")
	}
	select {
	case <-store.renewed:
	case <-time.After(time.Second):
		t.Fatal("snapshot lease was not renewed")
	}
	close(backend.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("snapshot sync did not complete")
	}
	if !store.ready {
		t.Fatal("snapshot replica was not marked ready")
	}
	if store.renewCalls == 0 {
		t.Fatal("snapshot lease was not renewed")
	}
}

func TestRunnerLeaseRenewalLossCancelsSnapshotSync(t *testing.T) {
	store := &renewingReplicaStore{
		fakeReplicaStore: fakeReplicaStore{job: state.SnapshotReplicaJob{
			SnapshotID: "snap-lost", DeploymentID: "dep-lost", NodeID: "node-2", LeaseToken: "lease-lost",
			StorageKey: "snap/dep-lost/mem", VMStateStorageKey: "snap/dep-lost/vmstate",
		}},
		renewErr: state.ErrConflict,
	}
	backend := &leaseGateBackend{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
	r := New(store, backend, "node-2", slog.Default()).WithMaxPerTick(1).WithLeaseRenewInterval(time.Millisecond)
	done := make(chan struct{})
	go func() {
		r.runWorkTick(context.Background())
		close(done)
	}()
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("snapshot sync did not start")
	}
	select {
	case <-backend.canceled:
	case <-time.After(time.Second):
		t.Fatal("lease loss did not cancel snapshot sync")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not finish after lease loss")
	}
	if !errors.Is(store.failed, state.ErrConflict) {
		t.Fatalf("recorded failure = %v, want ErrConflict", store.failed)
	}
	if store.ready {
		t.Fatal("snapshot replica was marked ready after lease loss")
	}
}

func TestRunnerTickMissingDependencyIsPermanent(t *testing.T) {
	store := &fakeReplicaStore{job: state.SnapshotReplicaJob{
		SnapshotID: "snap-1", DeploymentID: "dep-1", NodeID: "node-2",
		StorageKey: "snap/dep-1/mem", VMStateStorageKey: "snap/dep-1/vmstate",
		LayerStorageKeys: []string{"apps/acme/dep-1.ext4"},
	}}
	backend := &fakeBackend{objects: map[string][]byte{
		"snap/dep-1/mem":     []byte("memory"),
		"snap/dep-1/vmstate": []byte("vmstate"),
	}}
	New(store, backend, "node-2", slog.Default()).runTick(context.Background())

	if store.failed == nil {
		t.Fatal("missing dependency was not recorded")
	}
	if !storage.IsNotFound(store.failed) {
		t.Fatalf("failure = %v, want wrapped storage.ErrNotFound", store.failed)
	}
}

func TestRunnerWorkTickDoesNotReconcile(t *testing.T) {
	store := &fakeReplicaStore{job: state.SnapshotReplicaJob{
		SnapshotID: "snap-1", DeploymentID: "dep-1", NodeID: "node-2", Region: "local",
		StorageKey: "snap/dep-1/mem", VMStateStorageKey: "snap/dep-1/vmstate",
	}}
	backend := &fakeBackend{objects: map[string][]byte{
		"snap/dep-1/mem":     []byte("memory"),
		"snap/dep-1/vmstate": []byte("vmstate"),
	}}
	r := New(store, backend, "node-2", slog.Default())
	r.runWorkTick(context.Background())

	if store.queued != 0 {
		t.Fatalf("work tick reconciled the full snapshot set: enqueue calls=%d", store.queued)
	}
	if !store.ready {
		t.Fatal("work tick did not process an already-enqueued replica")
	}
}

func TestSyncJobRejectsIncompleteKeys(t *testing.T) {
	job := state.SnapshotReplicaJob{StorageKey: "snap/dep/mem"}
	if err := syncJob(context.Background(), &fakeBackend{}, job); err == nil {
		t.Fatal("syncJob accepted incomplete storage keys")
	}
}

func TestSyncJobDoesNotReadResidentArtifacts(t *testing.T) {
	backend := &fakeLocalBackend{
		fakeBackend: &fakeBackend{objects: map[string][]byte{
			"snap/dep/vmstate": []byte("vmstate"),
		}},
		local: map[string]bool{
			"snap/dep/mem":       true,
			"apps/acme/dep.ext4": true,
		},
	}
	job := state.SnapshotReplicaJob{
		StorageKey: "snap/dep/mem", VMStateStorageKey: "snap/dep/vmstate",
		LayerStorageKeys: []string{"apps/acme/dep.ext4"},
	}
	if err := syncJob(context.Background(), backend, job); err != nil {
		t.Fatal(err)
	}
	if got, want := backend.gets, []string{"snap/dep/vmstate"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("Get calls = %v, want %v", got, want)
	}
	if len(backend.resolved) != 3 {
		t.Fatalf("LocalPath calls = %v, want all three keys", backend.resolved)
	}
}

func TestPrometheusMetricsPreinstantiatesClosedOutcomes(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics, err := NewPrometheusMetrics(reg, "europe-west3")
	if err != nil {
		t.Fatalf("NewPrometheusMetrics: %v", err)
	}
	metrics.ObserveFanout("ready", "europe-west3")
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	var b strings.Builder
	for _, mf := range mfs {
		if _, err := expfmt.MetricFamilyToText(&b, mf); err != nil {
			t.Fatalf("MetricFamilyToText: %v", err)
		}
	}
	body := b.String()
	for _, want := range []string{
		`snapshothipd_fanout_total{outcome="ready",region="europe-west3"} 1`,
		`snapshothipd_fanout_total{outcome="failed",region="europe-west3"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing pre-instantiated metric %q in:\n%s", want, body)
		}
	}
}

func TestPrometheusMetricsRecordsFanoutLatency(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics, err := NewPrometheusMetrics(reg, "europe-west3")
	if err != nil {
		t.Fatalf("NewPrometheusMetrics: %v", err)
	}
	metrics.ObserveFanoutLatency("europe-west3", 125*time.Millisecond)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	var b strings.Builder
	for _, mf := range mfs {
		if _, err := expfmt.MetricFamilyToText(&b, mf); err != nil {
			t.Fatalf("MetricFamilyToText: %v", err)
		}
	}
	body := b.String()
	for _, want := range []string{
		`snapshothipd_fanout_latency_seconds_bucket{region="europe-west3",le="0.2"} 1`,
		`snapshothipd_fanout_latency_seconds_count{region="europe-west3"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing latency metric %q in:\n%s", want, body)
		}
	}
}

// TestSyncJobSharesAFetchedDriveWithItsLayer pins ADR-633's replica half: a
// snapshot drive fetched whole from the parent hands its unchanged blocks
// back to the app layer, and a drive that was already local (the capturing
// node) or a periodic revalidation is left alone.
func TestSyncJobSharesAFetchedDriveWithItsLayer(t *testing.T) {
	const (
		mem   = "snap/dep/captures/c1/v2/mem"
		drive = "snap/dep/captures/c1/v2/drive"
		layer = "apps/acme/dep.ext4"
	)
	cases := []struct {
		name         string
		driveLocal   bool
		revalidation bool
		wantShare    bool
	}{
		{"fetched drive", false, false, true},
		{"drive captured on this node", true, false, false},
		{"revalidation", false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls [][2]string
			prev := shareUnchangedBlocks
			shareUnchangedBlocks = func(_ context.Context, _ storage.StorageBackend, key, base string) (int64, error) {
				calls = append(calls, [2]string{key, base})
				return 0, nil
			}
			t.Cleanup(func() { shareUnchangedBlocks = prev })
			backend := &fakeLocalBackend{
				fakeBackend: &fakeBackend{objects: map[string][]byte{
					"snap/dep/captures/c1/v2/vmstate": []byte("vmstate"),
					drive:                             []byte("drive"),
				}},
				local: map[string]bool{mem: true, layer: true, drive: tc.driveLocal},
			}
			job := state.SnapshotReplicaJob{
				StorageKey: mem, VMStateStorageKey: "snap/dep/captures/c1/v2/vmstate",
				LayerStorageKeys: []string{layer}, Revalidation: tc.revalidation,
			}
			r := New(&fakeReplicaStore{job: job}, backend, "node-2", slog.Default())
			r.leaseRenewInterval = 0
			if err := r.syncJob(context.Background(), job); err != nil && !tc.revalidation {
				t.Fatal(err)
			}
			if got := len(calls) == 1 && calls[0] == [2]string{drive, layer}; got != tc.wantShare {
				t.Fatalf("share calls = %v, want shared=%v", calls, tc.wantShare)
			}
		})
	}
}
