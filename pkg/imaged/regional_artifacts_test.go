package imaged

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestRegionalArtifactPublicationAndCleanup(t *testing.T) {
	ctx := t.Context()
	harness := newTestHarness(t, state.DeploymentKindImage, api.PlanPro, "")
	store := harness.store
	id := "eu-fsn"
	if _, err := store.UpsertComputeRegion(ctx, state.ComputeRegion{ID: id, DisplayName: "Falkenstein", AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "regional-artifact-worker", Region: &id, Active: true}); err != nil {
		t.Fatal(err)
	}
	appSpec := harness.app
	appSpec.ID, appSpec.Slug, appSpec.NodeID, appSpec.ComputeRegion = "", "regional-artifacts", "", id
	app, err := store.CreateApp(ctx, appSpec)
	if err != nil {
		t.Fatal(err)
	}
	depSpec := harness.dep
	depSpec.ID, depSpec.AppID = "", app.ID
	dep, err := store.CreateDeployment(ctx, depSpec)
	if err != nil {
		t.Fatal(err)
	}
	legacy, regional := mustLocalStorage(t, t.TempDir()), mustLocalStorage(t, t.TempDir())
	router, err := storage.NewRegionRouter(map[string]storage.StorageBackend{id: regional}, legacy)
	if err != nil {
		t.Fatal(err)
	}
	h := New(store, harness.notif, fakePuller{digest: "sha256:abc", cfg: oci.ImageConfig{Cmd: []string{"./app"}}}, harness.bld, "./init", t.TempDir(), silentLogger()).WithStorage(router)
	if err := h.HandleNotification(ctx, db.Notification{Channel: db.NotifyDeploymentChanged, Payload: `{"app_id":"` + app.ID + `","to":"` + dep.ID + `","kind":"image","image_digest":"sha256:abc"}`}); err != nil {
		t.Fatal(err)
	}
	dep, err = store.DeploymentByID(ctx, dep.ID)
	want := region.ArtifactKey(id, sched.AppLayerKey(app.Slug, dep.ID))
	if err != nil || dep.RootfsKey != want {
		t.Fatalf("published=%s err=%v", dep.RootfsKey, err)
	}
	if _, err := legacy.Get(ctx, sched.AppLayerKey(app.Slug, dep.ID)); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("layer escaped to legacy: %v", err)
	}
	path, cleanup, err := h.stageScanExt4(ctx, router, app, dep)
	if err != nil || path == "" {
		t.Fatalf("regional scan staging: %s %v", path, err)
	}
	cleanup()
	memKey := region.ArtifactKey(id, state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "capture"))
	snapshot := state.Snapshot{DeploymentID: dep.ID, StorageKey: memKey, FCVersion: "1.10.0"}
	for _, key := range []string{memKey, state.SnapshotVMStateKey(snapshot), state.SnapshotDriveKey(snapshot)} {
		if err := router.Put(ctx, key, strings.NewReader("snapshot")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListSnapshotsForGC(ctx)
	if err != nil || len(rows) != 1 || rows[0].RootfsKey != dep.RootfsKey {
		t.Fatalf("GC projection=%+v err=%v", rows, err)
	}
	if err := h.cleanupDeploymentFiles(ctx, dep.ID, false); err != nil {
		t.Fatal(err)
	}
	keys, err := regional.(storage.LocalArtifactLister).List(ctx, "")
	if err != nil || len(keys) != 0 {
		t.Fatalf("regional cleanup left %v: %v", keys, err)
	}

	job, err := store.JobCreateScheduledIfUnderQuota(ctx, state.Job{AccountID: app.AccountID, Name: "regional-materialize", Kind: "batch", ImageRef: "registry.example/worker:v1", Command: []string{"/bin/true"}, RAMMB: 128, TaskTimeoutS: 60, MaxParallelism: 1, ComputeRegion: id}, 10)
	if err != nil {
		t.Fatal(err)
	}
	puller := &jobMaterializationPuller{digest: "sha256:" + strings.Repeat("a", 64), manifest: oci.Manifest{Layers: []oci.Descriptor{{Digest: "sha256:" + strings.Repeat("b", 64)}}}, blob: []byte("placeholder")}
	h = New(store, harness.notif, puller, harness.bld, "./init", t.TempDir(), silentLogger()).WithStorage(router)
	if err := h.MaterializeJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	job, err = store.JobGetByID(ctx, job.ID)
	if err != nil || !strings.HasPrefix(job.ImageStorageKey, "regions/"+id+"/jobs/") {
		t.Fatalf("job artifact=%s err=%v", job.ImageStorageKey, err)
	}
	if err := h.deleteJobArtifact(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := router.Get(ctx, job.ImageStorageKey); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("job cleanup: %v", err)
	}
}

func TestPressureGCLeavesLiveRegionalLayerColdBootable(t *testing.T) {
	row := state.SnapshotForGC{
		ID: "snapshot", AppID: "app", AccountID: "account", DeploymentID: "dep", AppSlug: "app",
		RootfsKey: "regions/eu-fsn/apps/app/dep.ext4", StorageKey: state.WarmSnapMemKey("dep"),
		Tier: state.SnapshotTierWarm, MemBytes: 4096, DeploymentStatus: state.DeployLive,
	}
	// Warm captures for a warm-disabled app can be evicted under pressure,
	// but the deletion target must retain the live-layer protection metadata.
	targets := evictOldestFromHeaviestAccount([]state.SnapshotForGC{row})
	if len(targets) != 1 || targets[0].DeploymentStatus != state.DeployLive || targets[0].RootfsKey != row.RootfsKey {
		t.Fatalf("pressure target lost cold-boot retention metadata: %+v", targets)
	}
	ctx := t.Context()
	store := state.NewMemStore()
	legacy, regional := mustLocalStorage(t, t.TempDir()), mustLocalStorage(t, t.TempDir())
	router, _ := storage.NewRegionRouter(map[string]storage.StorageBackend{"eu-fsn": regional}, legacy)
	if err := router.Put(ctx, row.RootfsKey, strings.NewReader("live layer")); err != nil {
		t.Fatal(err)
	}
	handler := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithStorage(router)
	loop := &Loop{store: store, handler: handler, log: silentLogger()}
	if err := loop.deleteSnapshotsAndFiles(ctx, targets); err != nil {
		t.Fatal(err)
	}
	reader, err := router.Get(ctx, row.RootfsKey)
	if err != nil {
		t.Fatalf("pressure GC deleted live regional layer: %v", err)
	}
	_ = reader.Close()
}
