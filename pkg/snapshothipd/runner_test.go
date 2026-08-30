package snapshothipd

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

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

func (f *fakeReplicaStore) MarkSnapshotReplicaReady(context.Context, string, string) error {
	f.ready = true
	return nil
}

func (f *fakeReplicaStore) MarkSnapshotReplicaFailed(_ context.Context, _, _ string, err error) error {
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

type fakeMetrics struct{ outcomes []string }

func (f *fakeMetrics) ObserveFanout(outcome, region string) {
	f.outcomes = append(f.outcomes, outcome+":"+region)
}

func TestRunnerTickPrepositionsCompleteRestoreClosure(t *testing.T) {
	store := &fakeReplicaStore{job: state.SnapshotReplicaJob{
		SnapshotID: "snap-1", DeploymentID: "dep-1", NodeID: "node-2", Region: "europe-west3",
		StorageKey: "snap/dep-1/mem", VMStateStorageKey: "snap/dep-1/vmstate",
		LayerStorageKeys: []string{"apps/acme/dep-1.ext4", "apps/acme/dep-1-metrics.ext4"}, Attempts: 1,
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
