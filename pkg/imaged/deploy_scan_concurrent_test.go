package imaged

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// grypeStubDelay stands in for Grype's run time in stubs: long enough that
// hashing a test artifact and finding a reusable result finish first, so a
// reuse hit cancels the scan, as on production where both take seconds.
func grypeStubDelay(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(200 * time.Millisecond):
		return nil
	}
}

// The per-deploy scan runs Grype beside the artifact hash instead of after it
// (deploy-path review P15): Grype starts before the hash finishes, a reuse hit
// cancels it, and the scan waits for it to exit before the staged artifact is
// removed.
func TestDeployScanRunsGrypeBesideTheArtifactHash(t *testing.T) {
	appsRoot := t.TempDir()
	var started, cancelled, running atomic.Int32
	release := make(chan struct{})
	stubGrype := func(ctx context.Context, _ string) (*ScanResult, error) {
		started.Add(1)
		running.Add(1)
		defer running.Add(-1)
		select {
		case <-ctx.Done():
			cancelled.Add(1)
			return nil, ctx.Err()
		case <-release:
		}
		return &ScanResult{
			ScannerVersion: "0.116.0", ScannerDBStatus: "valid",
			ScannerDBVersion: "v6.1.2", ScannerDBBuiltAt: "2026-10-28T00:00:00Z",
		}, nil
	}
	backend, err := storage.NewLocalStorageBackend(appsRoot)
	if err != nil {
		t.Fatal(err)
	}
	h := New(state.NewMemStore(), &fakeNotifier{}, fakePuller{}, &fakeBuilder{},
		"./init", appsRoot, slog.New(slog.DiscardHandler)).
		WithStorage(backend).
		WithGrypeRun(stubGrype)
	ctx := context.Background()
	acct, err := h.store.CreateAccount(ctx, "concurrent@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := h.store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "concurrent", RAMMB: 512, IdleTimeoutS: 60, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(appsRoot+"/concurrent", 0o755); err != nil {
		t.Fatal(err)
	}
	deploy := func() state.Deployment {
		t.Helper()
		dep, err := h.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:abc", Kind: state.DeploymentKindImage})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(appsRoot+"/concurrent/"+dep.ID+".ext4", []byte("layer"), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = h.runDeployScan(ctx, app, dep)
		dep, err = h.store.DeploymentByID(ctx, dep.ID)
		if err != nil {
			t.Fatal(err)
		}
		return dep
	}

	// First deploy: nothing to reuse, so the scan waits for Grype, which was
	// already running when the hash finished.
	go func() {
		for started.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		close(release)
	}()
	first := deploy()
	if first.ScanStatus != "complete" || started.Load() != 1 || cancelled.Load() != 0 {
		t.Fatalf("first scan: status=%s started=%d cancelled=%d, want complete 1 0", first.ScanStatus, started.Load(), cancelled.Load())
	}

	// Second deploy of the same bytes: the reuse hit cancels the running
	// Grype, and runDeployScan returns only after it exited.
	release = make(chan struct{}) // never closed: only cancellation ends it
	second := deploy()
	if got := decodeScanEvidence(second.ScanResult); got == nil || got.ReusedFrom != first.ID {
		t.Fatalf("second scan = %s, want reuse of %s", second.ScanResult, first.ID)
	}
	if started.Load() != 2 || cancelled.Load() != 1 || running.Load() != 0 {
		t.Fatalf("second scan: started=%d cancelled=%d running=%d, want 2 1 0", started.Load(), cancelled.Load(), running.Load())
	}
}
