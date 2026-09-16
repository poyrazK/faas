package snapshothipd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

const prewarmQualificationCycles = 100

// qualificationBackend is a small registry-shaped backend that tracks both
// parent reads and closes. The runner must close every response body even when
// the read-through cache is under eviction pressure.
type qualificationBackend struct {
	objects map[string][]byte
	gets    atomic.Int64
	closes  atomic.Int64
}

func (b *qualificationBackend) Put(context.Context, string, io.Reader) error { return nil }

func (b *qualificationBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, ok := b.objects[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	b.gets.Add(1)
	return &qualificationReadCloser{
		Reader: bytes.NewReader(data),
		closeFn: func() {
			b.closes.Add(1)
		},
	}, nil
}

func (b *qualificationBackend) Delete(context.Context, string) error { return nil }

type qualificationReadCloser struct {
	*bytes.Reader
	closeFn func()
}

func (r *qualificationReadCloser) Close() error {
	r.closeFn()
	return nil
}

func TestSnapshotPrewarmQualificationTwoNodeChurn(t *testing.T) {
	// This is intentionally a bounded qualification drill rather than a
	// benchmark. It exercises 100 independent snapshot claims on two nodes,
	// while both caches stay under their byte budget. The stale-lease fence and
	// lease-loss cancellation are pinned by the state and runner tests next to
	// this one; this test verifies that the complete ready path remains safe
	// under the same churn and that resource ownership is balanced.
	ctx := context.Background()
	store := state.NewMemStore()
	region := state.DefaultLocalityLabel
	peer := state.ComputeNode{
		ID: "qualification-peer", Name: "qualification-peer",
		TargetURL:          "unix:///run/faas/qualification-peer.sock",
		AdmissionCeilingMB: 4096, VCPUBudget: 16, Active: true,
		Region: &region,
	}
	if _, err := store.CreateComputeNode(ctx, peer); err != nil {
		t.Fatalf("CreateComputeNode(peer): %v", err)
	}
	origin, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("ComputeNodeByName(origin): %v", err)
	}
	account, err := store.CreateAccount(ctx, "prewarm-qualification@example.com", "pro")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "prewarm-qualification",
		RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	const blobSize = 4096
	backend := &qualificationBackend{objects: make(map[string][]byte, prewarmQualificationCycles*3)}
	blob := bytes.Repeat([]byte("x"), blobSize)

	// Keep the budget finite but out of the hot path for this latency drill.
	// The storage package separately pins oldest-entry eviction; coupling that
	// filesystem walk to the p50 acceptance check would measure eviction cost,
	// not prewarm latency.
	const cacheBudget = int64(16 << 20)
	originCache, err := storage.NewLocalCacheBackend(backend, filepath.Join(t.TempDir(), "origin-cache"), cacheBudget)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend(origin): %v", err)
	}
	peerCache, err := storage.NewLocalCacheBackend(backend, filepath.Join(t.TempDir(), "peer-cache"), cacheBudget)
	if err != nil {
		_ = originCache.Close()
		t.Fatalf("NewLocalCacheBackend(peer): %v", err)
	}
	defer func() {
		if err := originCache.Close(); err != nil {
			t.Errorf("Close(origin cache): %v", err)
		}
		if err := peerCache.Close(); err != nil {
			t.Errorf("Close(peer cache): %v", err)
		}
	}()

	originMetrics := &fakeMetrics{}
	peerMetrics := &fakeMetrics{}
	originRunner := New(store, originCache, origin.ID, slog.Default()).
		WithMaxPerTick(prewarmQualificationCycles).WithMetrics(originMetrics)
	peerRunner := New(store, peerCache, peer.ID, slog.Default()).
		WithMaxPerTick(prewarmQualificationCycles).WithMetrics(peerMetrics)
	for i := 0; i < prewarmQualificationCycles; i++ {
		deploymentID := fmt.Sprintf("qualification-deployment-%03d", i)
		deployment, err := store.CreateDeployment(ctx, state.Deployment{
			ID: deploymentID, AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: fmt.Sprintf("sha256:qualification-%03d", i), Status: state.DeployLive,
		})
		if err != nil {
			t.Fatalf("CreateDeployment(%d): %v", i, err)
		}
		if err := store.UpdateDeploymentStatus(ctx, deployment.ID, state.DeployLive, ""); err != nil {
			t.Fatalf("UpdateDeploymentStatus(%d): %v", i, err)
		}
		snapshot, err := store.CreateSnapshot(ctx, state.Snapshot{
			ID: fmt.Sprintf("qualification-snapshot-%03d", i), DeploymentID: deployment.ID,
			FCVersion: "fc-qualification", StorageKey: state.SnapMemKey(deployment.ID),
		})
		if err != nil {
			t.Fatalf("CreateSnapshot(%d): %v", i, err)
		}
		if err := store.RecordSnapshotOrigin(ctx, snapshot.ID, origin.ID); err != nil {
			t.Fatalf("RecordSnapshotOrigin(%d): %v", i, err)
		}
		backend.objects[snapshot.StorageKey] = blob
		backend.objects[state.SnapVMStateKey(deployment.ID)] = blob
		backend.objects["layers/"+deployment.ID+".ext4"] = blob

		// Process one new snapshot per node before publishing the next one.
		// This measures queue-to-ready latency for each cycle instead of
		// measuring the intentional backlog age of a 100-item batch.
		originRunner.runTick(ctx)
		peerRunner.runTick(ctx)
	}

	if got, want := backend.gets.Load(), int64(prewarmQualificationCycles*3*2); got != want {
		t.Fatalf("parent reads = %d, want %d (three blobs on each node)", got, want)
	}
	if got := backend.closes.Load(); got != backend.gets.Load() {
		t.Fatalf("parent response closes = %d, want %d", got, backend.gets.Load())
	}
	assertPrewarmQualificationLatency(t, "origin", originMetrics.latencies)
	assertPrewarmQualificationLatency(t, "peer", peerMetrics.latencies)

	for i := 0; i < prewarmQualificationCycles; i++ {
		snapshotID := fmt.Sprintf("qualification-snapshot-%03d", i)
		ready, err := store.ReadySnapshotReplicaNodes(ctx, snapshotID)
		if err != nil {
			t.Fatalf("ReadySnapshotReplicaNodes(%d): %v", i, err)
		}
		if len(ready) != 2 || !containsString(ready, origin.ID) || !containsString(ready, peer.ID) {
			t.Fatalf("ready nodes for %s = %v, want [%s %s]", snapshotID, ready, origin.ID, peer.ID)
		}
		locality, err := store.SnapshotLocalityFor(ctx, snapshotID)
		if err != nil {
			t.Fatalf("SnapshotLocalityFor(%d): %v", i, err)
		}
		if locality.OriginNodeID != origin.ID || len(locality.ReadyNodeIDs) != 2 {
			t.Fatalf("locality for %s = %+v, want origin=%s and two ready nodes", snapshotID, locality, origin.ID)
		}
	}

	for name, cache := range map[string]*storage.LocalCacheBackend{
		"origin": originCache,
		"peer":   peerCache,
	} {
		bytesOnDisk, temporary, err := qualificationCacheUsage(cache.Root())
		if err != nil {
			t.Fatalf("%s cache usage: %v", name, err)
		}
		if bytesOnDisk > cacheBudget {
			t.Fatalf("%s cache grew to %d bytes, budget is %d", name, bytesOnDisk, cacheBudget)
		}
		if temporary != 0 {
			t.Fatalf("%s cache left %d temporary files behind", name, temporary)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertPrewarmQualificationLatency(t *testing.T, node string, samples []time.Duration) {
	t.Helper()
	if len(samples) != prewarmQualificationCycles {
		t.Fatalf("%s latency samples = %d, want %d", node, len(samples), prewarmQualificationCycles)
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p50 := sorted[(len(sorted)-1)/2]
	t.Logf("%s prewarm p50=%s p95=%s", node, p50, sorted[len(sorted)*95/100])
	if p50 > 200*time.Millisecond {
		t.Fatalf("%s prewarm p50 = %s, want <= 200ms", node, p50)
	}
}

func qualificationCacheUsage(root string) (bytesOnDisk int64, temporary int, err error) {
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".faas-cache-") {
			temporary++
			return nil
		}
		if strings.HasSuffix(name, ".meta") || strings.HasSuffix(name, ".generation") {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		bytesOnDisk += info.Size()
		return nil
	})
	return bytesOnDisk, temporary, err
}
