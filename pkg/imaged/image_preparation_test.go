package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

type crashingImageStore struct {
	state.Store
	state.DeploymentImagePreparationStore
	state.DeploymentActivationLocker
	crash string
}

func (s *crashingImageStore) PublishImagePreparationLayer(ctx context.Context, id, token, path, key string, bytes int64) error {
	err := s.DeploymentImagePreparationStore.PublishImagePreparationLayer(ctx, id, token, path, key, bytes)
	if err == nil && s.crash == "layer_published" {
		panic("simulated imaged crash")
	}
	return err
}
func (s *crashingImageStore) AdvanceImagePreparation(ctx context.Context, id, token string, from, to state.ImagePreparationPhase) error {
	if s.crash == "handoff_emitted" && to == state.ImageHandedOff {
		panic("simulated imaged crash")
	}
	err := s.DeploymentImagePreparationStore.AdvanceImagePreparation(ctx, id, token, from, to)
	if err == nil && s.crash == string(to) {
		panic("simulated imaged crash")
	}
	return err
}
func (s *crashingImageStore) TransitionImagePreparation(ctx context.Context, id, token string, next state.DeploymentStatus) error {
	err := s.DeploymentImagePreparationStore.TransitionImagePreparation(ctx, id, token, next)
	if err == nil && s.crash == "snapshotting" && next == state.DeploySnapshotting {
		panic("simulated imaged crash")
	}
	return err
}

func TestImagePreparationResumesAfterCrash(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			for _, point := range []string{"assembly", "layer_published", "scan_complete", "snapshotting", "handoff_emitted"} {
				t.Run(point, func(t *testing.T) {
					store, app, dep := imagePreparationFixture(t, backend)
					images := store.(state.DeploymentImagePreparationStore)
					crashing := &crashingImageStore{Store: store, DeploymentImagePreparationStore: images, DeploymentActivationLocker: store.(state.DeploymentActivationLocker), crash: point}
					builder := &fakeBuilder{bytesOut: 9}
					if point == "assembly" {
						builder.buildHook = func() { panic("simulated imaged crash") }
					}
					notif := &fakeNotifier{}
					scans := 0
					h := imagePreparationHandler(t, crashing, builder, notif, &scans)
					assertImageCrash(t, func() {
						_ = h.handleSnapshotBoot(context.Background(), snapshotBootPayload{AppID: app.ID, DeploymentID: dep.ID, NodeID: "node-a"})
					})
					stopped, err := store.DeploymentByID(context.Background(), dep.ID)
					if err != nil || stopped.Status.IsTerminal() {
						t.Fatalf("crash marked candidate terminal: %s %v", stopped.Status, err)
					}
					if point == "assembly" && stopped.RootfsPath != "example.test/app@sha256:abc" {
						t.Fatalf("unfinished assembly released input: %q", stopped.RootfsPath)
					}
					builder.buildHook = nil
					// A new handler and loop recover from PostgreSQL/MemStore, without the
					// original event or an in-process copy of the preparation.
					restarted := imagePreparationHandler(t, store, builder, notif, &scans)
					restarted.appsRoot = h.appsRoot
					loop := NewLoop(LoopConfig{Handler: restarted, Store: store})
					loop.recoverBuildHandoffs(context.Background())
					got, err := store.DeploymentByID(context.Background(), dep.ID)
					if err != nil || got.Status != state.DeploySnapshotting {
						t.Fatalf("restart did not hand off: %s %v", got.Status, err)
					}
					var stages state.StageState
					if err := json.Unmarshal(got.StageState, &stages); err != nil || stages.Current != state.StageSnapshotPrepare {
						t.Fatalf("restart left stale stage: current=%s err=%v", stages.Current, err)
					}
					wantBuilds := 1
					if point == "assembly" {
						wantBuilds = 2
					}
					if len(builder.calls) != wantBuilds {
						t.Fatalf("builds=%d want=%d", len(builder.calls), wantBuilds)
					}
					if scans != 1 {
						t.Fatalf("completed scan repeated: scans=%d", scans)
					}
					if findNotify(notif, db.NotifySnapshotPrime) == nil {
						t.Fatal("no snapshot handoff")
					}
					before := len(notif.calls)
					loop.recoverBuildHandoffs(context.Background())
					if len(notif.calls) != before || len(builder.calls) != wantBuilds {
						t.Fatal("completed checkpoint replayed")
					}
				})
			}
		})
	}
}

func imagePreparationFixture(t *testing.T, backend string) (state.Store, state.App, state.Deployment) {
	t.Helper()
	var store state.Store = state.NewMemStore()
	ctx := context.Background()
	if backend == "postgres" {
		pool := pgtest.OpenMigrated(t)
		if err := db.MigrateUp(ctx, pool); err != nil {
			t.Fatal(err)
		}
		store = state.NewPgStore(pool)
	}
	acct, err := store.CreateAccount(ctx, "image-restart@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "image-restart", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "example.test/app@sha256:abc"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, dep.ID, dep.ImageDigest, "builder/input", 12); err != nil {
		t.Fatal(err)
	}
	return store, app, dep
}

func imagePreparationHandler(t *testing.T, store state.Store, builder *fakeBuilder, notif Notifier, scans *int) *Handler {
	t.Helper()
	return New(store, notif, fakePuller{digest: "sha256:abc", cfg: oci.ImageConfig{Cmd: []string{"./app"}}}, builder, "./init", t.TempDir(), silentLogger()).WithNodeName("node-a").WithGrypeRun(func(context.Context, string) (*ScanResult, error) { *scans++; return &ScanResult{}, nil })
}

func assertImageCrash(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if recover() != "simulated imaged crash" {
			t.Fatal("did not simulate imaged crash")
		}
	}()
	f()
}

func TestImagePreparationShutdownLeavesRecoverableInput(t *testing.T) {
	store, app, dep := imagePreparationFixture(t, "memory")
	ctx, cancel := context.WithCancel(context.Background())
	builder := &fakeBuilder{bytesOut: 9, buildHook: cancel}
	scans := 0
	h := imagePreparationHandler(t, store, builder, &fakeNotifier{}, &scans)
	if err := h.handleSnapshotBoot(ctx, snapshotBootPayload{AppID: app.ID, DeploymentID: dep.ID}); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown: %v", err)
	}
	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status != state.DeployImaging || got.RootfsPath != dep.ImageDigest {
		t.Fatalf("shutdown lost source: status=%s path=%q", got.Status, got.RootfsPath)
	}
	builder.buildHook = nil
	if err := h.handleSnapshotBoot(context.Background(), snapshotBootPayload{DeploymentID: dep.ID}); err != nil {
		t.Fatal(err)
	}
}

func TestImagePreparationSerializesDuplicateDelivery(t *testing.T) {
	store, app, dep := imagePreparationFixture(t, "postgres")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	builder := &fakeBuilder{bytesOut: 9, buildHook: func() { close(entered); <-release }}
	scans := 0
	h := imagePreparationHandler(t, store, builder, &fakeNotifier{}, &scans)
	payload := snapshotBootPayload{AppID: app.ID, DeploymentID: dep.ID, NodeID: "node-a"}
	done := make(chan error, 2)
	go func() { done <- h.handleSnapshotBoot(ctx, payload) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("build did not start")
	}
	go func() { done <- h.handleSnapshotBoot(ctx, payload) }()
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if len(builder.calls) != 1 || scans != 1 {
		t.Fatalf("duplicate work: builds=%d scans=%d", len(builder.calls), scans)
	}
}

func TestImagePreparationRetriesSnapshotHandoff(t *testing.T) {
	store, app, dep := imagePreparationFixture(t, "memory")
	builder := &fakeBuilder{bytesOut: 9}
	scans := 0
	h := imagePreparationHandler(t, store, builder, &failingNotifier{}, &scans)
	if err := h.handleSnapshotBoot(context.Background(), snapshotBootPayload{AppID: app.ID, DeploymentID: dep.ID}); !errors.Is(err, errImagePreparationRecovery) {
		t.Fatalf("handoff not retryable: %v", err)
	}
	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status != state.DeploySnapshotting {
		t.Fatalf("handoff failure killed prepared image: %s", got.Status)
	}
	h.notif = &fakeNotifier{}
	if err := h.handleSnapshotBoot(context.Background(), snapshotBootPayload{DeploymentID: dep.ID}); err != nil {
		t.Fatal(err)
	}
	if len(builder.calls) != 1 || scans != 1 {
		t.Fatal("handoff retry repeated assembly or scan")
	}
}

func TestImagePreparationDirectImageRecoversWithoutBuildRow(t *testing.T) {
	store, app, dep := imagePreparationFixture(t, "postgres")
	if err := store.SetDeploymentRootfs(context.Background(), dep.ID, "", "builder/input", 0); err != nil {
		t.Fatal(err)
	}
	crashing := &crashingImageStore{Store: store, DeploymentImagePreparationStore: store.(state.DeploymentImagePreparationStore), DeploymentActivationLocker: store.(state.DeploymentActivationLocker), crash: "layer_published"}
	builder := &fakeBuilder{bytesOut: 9}
	scans := 0
	notif := &fakeNotifier{}
	h := imagePreparationHandler(t, crashing, builder, notif, &scans)
	assertImageCrash(t, func() {
		_ = h.handleDeployment(context.Background(), deploymentChangedPayload{AppID: app.ID, To: dep.ID, Kind: string(state.DeploymentKindImage)})
	})
	restarted := imagePreparationHandler(t, store, builder, notif, &scans)
	restarted.appsRoot = h.appsRoot
	NewLoop(LoopConfig{Handler: restarted, Store: store}).recoverBuildHandoffs(context.Background())
	got, err := store.DeploymentByID(context.Background(), dep.ID)
	if err != nil || got.Status != state.DeploySnapshotting || len(builder.calls) != 1 || scans != 1 {
		t.Fatalf("direct image restart: status=%s builds=%d scans=%d err=%v", got.Status, len(builder.calls), scans, err)
	}
}

func TestPreparedImageScanRefreshesInvalidEvidence(t *testing.T) {
	for _, reason := range []string{"fresh", "stale", "changed_artifact", "missing_result", "policy_tightened"} {
		t.Run(reason, func(t *testing.T) {
			store, app, dep := imagePreparationFixture(t, "memory")
			app.SecurityPolicy = api.AppSecurityPolicyEnforce
			scans := 0
			h := imagePreparationHandler(t, store, &fakeBuilder{}, &fakeNotifier{}, &scans)
			path := h.appsRootPath(app.Slug, dep.ID)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("published ext4"), 0600); err != nil {
				t.Fatal(err)
			}
			digest, err := digestScanArtifact(path)
			if err != nil {
				t.Fatal(err)
			}
			evidence := func() *ScanResult {
				return &ScanResult{ImageDigest: dep.ImageDigest, ArtifactDigest: digest,
					ScannedAt: time.Now().UTC().Format(time.RFC3339Nano), ScannerVersion: "test", ScannerDBVersion: "test", ScannerDBStatus: "valid", ScannerDBBuiltAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)}
			}
			prior := evidence()
			switch reason {
			case "stale":
				prior.ScannedAt = time.Now().Add(-verifiedScanMaxAge - time.Minute).Format(time.RFC3339Nano)
			case "changed_artifact":
				prior.ArtifactDigest = "sha256:old"
			case "policy_tightened":
				prior.ScannerDBStatus = "invalid"
			}
			if reason == "missing_result" {
				prior = nil
			}
			raw, err := json.Marshal(prior)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpsertDeploymentScanResult(context.Background(), dep.ID, raw, "complete"); err != nil {
				t.Fatal(err)
			}
			dep, err = store.DeploymentByID(context.Background(), dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) { scans++; return evidence(), nil })
			if err := h.validatePreparedImageScan(context.Background(), app, dep); err != nil {
				t.Fatal(err)
			}
			want := 1
			if reason == "fresh" {
				want = 0
			}
			if scans != want {
				t.Fatalf("scan calls=%d want=%d", scans, want)
			}
		})
	}
}

func TestImagePreparationRechecksTightenedSecurityPolicy(t *testing.T) {
	store, app, dep := imagePreparationFixture(t, "memory")
	crashing := &crashingImageStore{Store: store, DeploymentImagePreparationStore: store.(state.DeploymentImagePreparationStore), DeploymentActivationLocker: store.(state.DeploymentActivationLocker), crash: "scan_complete"}
	builder := &fakeBuilder{bytesOut: 9}
	scans := 0
	notif := &fakeNotifier{}
	h := imagePreparationHandler(t, crashing, builder, notif, &scans)
	assertImageCrash(t, func() { _ = h.handleSnapshotBoot(context.Background(), snapshotBootPayload{DeploymentID: dep.ID}) })
	policy := api.AppSecurityPolicyEnforce
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{SecurityPolicy: &policy, SetSecurityPolicy: true}); err != nil {
		t.Fatal(err)
	}
	restarted := imagePreparationHandler(t, store, builder, notif, &scans)
	restarted.appsRoot = h.appsRoot
	if err := restarted.handleSnapshotBoot(context.Background(), snapshotBootPayload{DeploymentID: dep.ID}); !errors.Is(err, errSecurityScanBlocked) {
		t.Fatalf("tightened policy bypassed: %v", err)
	}
	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status != state.DeployFailed || got.ErrorCode != api.CodeSecurityScanBlocked || findNotify(notif, db.NotifySnapshotPrime) != nil {
		t.Fatalf("unsafe recovery: status=%s code=%s", got.Status, got.ErrorCode)
	}
}
