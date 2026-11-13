package imaged

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261020085706 — amendment 14: a release serving again after a
// rollback carries scan evidence from when it last served. The enforce lease
// rescans it before parking the app.

type rolledBackEvidenceFixture struct {
	store *state.MemStore
	app   state.App
	dep   state.Deployment
	loop  *Loop
	clock time.Time
}

func newRolledBackEvidence(t *testing.T, installed installedScanner) *rolledBackEvidenceFixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "rollback-lease@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "rollback-lease", Status: state.AppActive, SecurityPolicy: api.AppSecurityPolicyEnforce})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	built := start.Add(-12 * time.Hour).Format(time.RFC3339Nano)
	scanned := start.Add(-9 * time.Hour) // last scanned when it last served
	evidence, err := json.Marshal(ScanResult{
		ImageDigest: "sha256:rolled-back", ArtifactDigest: "sha256:artifact", ScannedAt: scanned.Format(time.RFC3339Nano),
		ScannerVersion: "0.116.0", ScannerDBStatus: "valid", ScannerDBVersion: "v6.1.10", ScannerDBBuiltAt: built,
		SeverityCounts: SeverityCounts{Low: 1}, Vulnerabilities: []Vulnerability{},
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:rolled-back", Status: state.DeployLive,
		ScanStatus: "complete", ScanResult: evidence, ScannedAt: scanned,
	})
	if err != nil {
		t.Fatal(err)
	}
	if installed.DBBuiltAt == "" {
		installed.DBBuiltAt = built
	}
	fx := &rolledBackEvidenceFixture{store: store, app: app, dep: dep, clock: start}
	fx.loop = &Loop{
		store: store,
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:   func() time.Time { return fx.clock },
		handler: (&Handler{store: store, notif: &fakeNotifier{}}).WithScannerIdentity(func(context.Context) (installedScanner, error) {
			return installed, nil
		}),
	}
	return fx
}

func (fx *rolledBackEvidenceFixture) appStatus(t *testing.T) state.AppStatus {
	t.Helper()
	got, err := fx.store.AppByID(context.Background(), fx.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got.Status
}

// The scanner and its database are the ones that made the evidence: the
// rescan renews it, and the app keeps serving.
func TestExpiredEvidenceIsRescannedBeforeTheLeaseParks(t *testing.T) {
	fx := newRolledBackEvidence(t, installedScanner{Version: "0.116.0", DBVersion: "v6.1.10"})
	ctx := context.Background()
	fx.loop.reconcileSecurityLeases(ctx, fx.clock, securityScanSweepEvery)
	if got := fx.appStatus(t); got != state.AppActive {
		t.Fatalf("app = %q after the first lease tick, want active while it is rescanned", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := fx.store.DeploymentByID(ctx, fx.dep.ID)
		if err != nil {
			t.Fatal(err)
		}
		if result := decodeScanEvidence(got.ScanResult); result != nil && result.VerifiedAt != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the lease did not rescan the expired evidence")
		}
		time.Sleep(10 * time.Millisecond)
	}
	fx.clock = fx.clock.Add(leaseRescanGrace + time.Minute)
	fx.loop.reconcileSecurityLeases(ctx, fx.clock, securityScanSweepEvery)
	if got := fx.appStatus(t); got != state.AppActive {
		t.Fatalf("app = %q after the rescan renewed its evidence, want active", got)
	}
}

// A rescan that cannot run leaves the evidence expired: the lease parks the
// app once the grace ends, as before.
func TestExpiredEvidenceParksAfterTheGraceWhenTheRescanFails(t *testing.T) {
	fx := newRolledBackEvidence(t, installedScanner{Version: "0.117.0", DBVersion: "v6.1.11"})
	ctx := context.Background()
	fx.loop.reconcileSecurityLeases(ctx, fx.clock, securityScanSweepEvery)
	if got := fx.appStatus(t); got != state.AppActive {
		t.Fatalf("app = %q after the first lease tick, want active during the grace", got)
	}
	fx.clock = fx.clock.Add(leaseRescanGrace / 2)
	fx.loop.reconcileSecurityLeases(ctx, fx.clock, securityScanSweepEvery)
	if got := fx.appStatus(t); got != state.AppActive {
		t.Fatalf("app = %q inside the grace, want active", got)
	}
	fx.clock = fx.clock.Add(leaseRescanGrace)
	fx.loop.reconcileSecurityLeases(ctx, fx.clock, securityScanSweepEvery)
	if got := fx.appStatus(t); got != state.AppEvictedCold {
		t.Fatalf("app = %q after the grace with expired evidence, want evicted_cold", got)
	}
}
