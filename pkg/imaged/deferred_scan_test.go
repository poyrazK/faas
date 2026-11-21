package imaged

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func deferredPreparationHandler(t *testing.T, store state.Store, builder *fakeBuilder, notif Notifier, grype func(context.Context, string) (*ScanResult, error)) *Handler {
	t.Helper()
	return New(store, notif, fakePuller{digest: "sha256:abc", cfg: oci.ImageConfig{Cmd: []string{"./app"}}}, builder, "./init", t.TempDir(), silentLogger()).
		WithNodeName("node-a").WithDeferredDeployScans().WithGrypeRun(grype)
}

// ADR-075 amendment 2026-11-17: under security_policy off the deploy is
// handed to snapshot preparation before its scan runs, and the scan still
// lands on the deployment afterwards.
func TestDeferredDeployScanRunsAfterHandoff(t *testing.T) {
	ctx := t.Context()
	store, app, dep := imagePreparationFixture(t, "memory")
	notif := &fakeNotifier{}
	release := make(chan struct{})
	h := deferredPreparationHandler(t, store, &fakeBuilder{bytesOut: 9}, notif, func(context.Context, string) (*ScanResult, error) {
		<-release
		return &ScanResult{}, nil
	})
	if err := h.handleSnapshotBoot(ctx, snapshotBootPayload{AppID: app.ID, DeploymentID: dep.ID, NodeID: "node-a"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil || got.Status != state.DeploySnapshotting || got.ScanStatus != "" {
		t.Fatalf("handoff waited for the scan: status=%s scan=%q err=%v", got.Status, got.ScanStatus, err)
	}
	if findNotify(notif, db.NotifySnapshotPrime) == nil {
		t.Fatal("no snapshot handoff")
	}
	if !h.deferredScans.running(dep.ID) {
		t.Fatal("deferred scan was not queued")
	}
	close(release)
	h.deferredScans.wait()
	got, err = store.DeploymentByID(ctx, dep.ID)
	if err != nil || got.ScanStatus != "complete" {
		t.Fatalf("deferred scan did not land: scan=%q err=%v", got.ScanStatus, err)
	}
	if h.deferredScans.running(dep.ID) {
		t.Fatal("finished scan still reported running")
	}
}

// The scan is queued only after the handoff is durable, so a crash at any
// checkpoint before it still scans the deployment exactly once.
func TestDeferredDeployScanRunsOnceAcrossCrash(t *testing.T) {
	for _, point := range []string{"layer_published", "scan_complete", "handoff_emitted"} {
		t.Run(point, func(t *testing.T) {
			ctx := t.Context()
			store, app, dep := imagePreparationFixture(t, "memory")
			images := store.(state.DeploymentImagePreparationStore)
			crashing := &crashingImageStore{Store: store, DeploymentImagePreparationStore: images, DeploymentActivationLocker: store.(state.DeploymentActivationLocker), crash: point}
			builder := &fakeBuilder{bytesOut: 9}
			notif := &fakeNotifier{}
			var scans atomic.Int32
			grype := func(context.Context, string) (*ScanResult, error) { scans.Add(1); return &ScanResult{}, nil }
			first := deferredPreparationHandler(t, crashing, builder, notif, grype)
			assertImageCrash(t, func() {
				_ = first.handleSnapshotBoot(ctx, snapshotBootPayload{AppID: app.ID, DeploymentID: dep.ID, NodeID: "node-a"})
			})
			first.deferredScans.wait()
			restarted := deferredPreparationHandler(t, store, builder, notif, grype)
			restarted.appsRoot = first.appsRoot
			NewLoop(LoopConfig{Handler: restarted, Store: store}).recoverBuildHandoffs(ctx)
			restarted.deferredScans.wait()
			got, err := store.DeploymentByID(ctx, dep.ID)
			if err != nil || got.Status != state.DeploySnapshotting || got.ScanStatus != "complete" {
				t.Fatalf("restart: status=%s scan=%q err=%v", got.Status, got.ScanStatus, err)
			}
			if n := scans.Load(); n != 1 {
				t.Fatalf("scans=%d want 1", n)
			}
		})
	}
}

// enrollmentStore answers an organization app's standard enrollment.
type enrollmentStore struct {
	MemStore   *state.MemStore
	enrollment state.ApplicationStandardEnrollment
	err        error
}

func (s *enrollmentStore) GetApplicationStandardEnrollment(context.Context, string, string) (state.ApplicationStandardEnrollment, error) {
	return s.enrollment, s.err
}

func TestDeployScanGatesHandoff(t *testing.T) {
	store, app, dep := imagePreparationFixture(t, "memory")
	mem := store.(*state.MemStore)
	enrolled := func(e state.ApplicationStandardEnrollment, err error) state.Store {
		return statetest.NewMem(&enrollmentStore{MemStore: mem, enrollment: e, err: err})
	}
	inOrg := func(a *state.App) { a.OrgID = "00000000-0000-4000-8000-000000000001" }
	for _, tc := range []struct {
		name   string
		store  state.Store
		serial bool
		mutate func(*state.App)
		gates  bool
	}{
		{"off defers", store, false, func(*state.App) {}, false},
		{"warn defers", store, false, func(a *state.App) { a.SecurityPolicy = api.AppSecurityPolicyWarn }, false},
		{"enforce gates", store, false, func(a *state.App) { a.SecurityPolicy = api.AppSecurityPolicyEnforce }, true},
		{"signed images gate", store, false, func(a *state.App) { a.RequireSigned = true }, true},
		// ADR-075 amendment 2026-11-19: an organization's app defers unless a
		// standard admits its instances natively.
		{"an organization's app without an enrollment defers", store, false, inOrg, false},
		{"an organization's app under no standard defers", enrolled(state.ApplicationStandardEnrollment{State: "persisted"}, nil), false, inOrg, false},
		{"an organization's app under a native standard gates", enrolled(state.ApplicationStandardEnrollment{PersistedRevision: 1}, nil), false, inOrg, true},
		{"an unanswered enrollment read gates", enrolled(state.ApplicationStandardEnrollment{}, errors.New("database unavailable")), false, inOrg, true},
		{"an unanswered producer lookup gates", store, false, func(a *state.App) { a.AccountID = "" }, true},
		{"without deferral every scan gates", store, true, func(*state.App) {}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := New(tc.store, &fakeNotifier{}, nil, nil, "", t.TempDir(), silentLogger())
			if !tc.serial {
				h = h.WithDeferredDeployScans()
			}
			a := app
			tc.mutate(&a)
			if got := h.deployScanGatesHandoff(t.Context(), a, dep); got != tc.gates {
				t.Fatalf("gates=%t want %t", got, tc.gates)
			}
		})
	}
}

func TestDeferredScansQueue(t *testing.T) {
	d := newDeferredScans()
	if !d.claim("dep-1") || d.claim("dep-1") {
		t.Fatal("a deployment's scan must be claimed once")
	}
	if !d.running("dep-1") || (*deferredScans)(nil).running("dep-1") {
		t.Fatal("running must report only claimed scans")
	}
	d.release("dep-1")
	if d.running("dep-1") || !d.claim("dep-1") {
		t.Fatal("a released scan can be queued again")
	}
	for i := 1; i < api.ImagedDeferredScanQueue; i++ {
		if !d.claim(fmt.Sprintf("dep-%d", i+1)) {
			t.Fatalf("claim %d refused below the bound", i+1)
		}
	}
	if d.claim("overflow") {
		t.Fatal("a full queue accepted another scan")
	}
}
