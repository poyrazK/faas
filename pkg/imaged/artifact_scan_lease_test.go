package imaged

// adr: 435. Simulated native views/Grype with real durable lease consumers.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/state"
)

func livePrivateLeaseFixture(t *testing.T) (*Loop, *testHarness) {
	t.Helper()
	h, th, _ := pipelineRuntimeFixture(t, true)
	if err := th.store.UpdateDeploymentStatus(t.Context(), th.dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	if err := h.runDeployScan(t.Context(), th.app, th.dep); err != nil {
		t.Fatal(err)
	}
	return &Loop{store: th.store, handler: h, log: silentLogger()}, th
}

func setPrivateLeaseHighFinding(report *api.ScanResult) {
	report.SeverityCounts = api.SeverityCounts{High: 1}
	report.Vulnerabilities = []api.Vulnerability{{ID: "CVE-fixture", Severity: "HIGH", Package: "fixture", Paths: []string{"/component/file"}}}
}

func TestPrivateSecurityLeasesUseComposedSetAndStorageClock(t *testing.T) {
	loop, th := livePrivateLeaseFixture(t)
	value, err := th.store.GetFreshDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
	if err != nil || len(value.Scan.Input.Reports) != 2 || len(value.Scan.Input.Artifacts) != 3 {
		t.Fatal("composed membership lost", err)
	}
	if _, err := th.store.GetFreshDeploymentArtifactScanEvidence(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		t.Fatal("fixture gained component evidence", err)
	}
	loop.reconcileSecurityLeases(t.Context(), time.Now().Add(48*time.Hour), time.Hour)
	app, err := th.store.AppByID(t.Context(), th.app.ID)
	if err != nil || app.Status != state.AppActive {
		t.Fatal("worker clock or missing component report quarantined clean runtime", err)
	}
	rows, err := th.store.ListDeploymentAudit(t.Context(), th.dep.ID, 10)
	if err != nil || len(rows) != 0 {
		t.Fatal("healthy composed lease wrote failure audit", err)
	}
}

func TestPrivateSecurityLeasesRefuseUnsafeOrStaleComposedEvidence(t *testing.T) {
	for _, mode := range []string{"unsafe main", "unsafe sidecar", "failed scan", "missing scan", "revoked signer", "metadata drift"} {
		t.Run(mode, func(t *testing.T) {
			var loop *Loop
			var th *testHarness
			if mode == "missing scan" {
				h, fixture, _ := pipelineRuntimeFixture(t, true)
				th = fixture
				loop = &Loop{store: th.store, handler: h, log: silentLogger()}
				if err := th.store.UpdateDeploymentStatus(t.Context(), th.dep.ID, state.DeployLive, ""); err != nil {
					t.Fatal(err)
				}
			} else {
				loop, th = livePrivateLeaseFixture(t)
			}
			reason := "security_scan_evidence_invalid"
			switch mode {
			case "unsafe main":
				publishComposedFixtureMutation(t, th, "", "complete", "HIGH")
				reason = "security_scan_regressed"
			case "unsafe sidecar":
				publishComposedFixtureMutation(t, th, "metrics", "complete", "UNKNOWN")
				reason = "security_scan_regressed"
			case "failed scan":
				publishComposedFixtureMutation(t, th, "", "failed", "")
			case "revoked signer":
				if err := th.store.DeleteAppTrustedSigner(t.Context(), th.app.AccountID, th.app.ID, "company"); err != nil {
					t.Fatal(err)
				}
				reason = "security_signature_revoked"
			case "metadata drift":
				if err := th.store.SetDeploymentRootfs(t.Context(), th.dep.ID, "/changed.ext4", "apps/changed.ext4", 1); err != nil {
					t.Fatal(err)
				}
			}
			loop.reconcileSecurityLeases(t.Context(), time.Now(), time.Hour)
			assertPrivateLeaseQuarantine(t, th, reason)
		})
	}
}

func assertPrivateLeaseQuarantine(t *testing.T, th *testHarness, reason string) {
	t.Helper()
	app, err := th.store.AppByID(t.Context(), th.app.ID)
	if err != nil || app.Status != state.AppEvictedCold {
		t.Fatalf("unsafe private evidence left app active: %v", err)
	}
	dep, err := th.store.DeploymentByID(t.Context(), th.dep.ID)
	if err != nil || dep.ParkedReason != string(state.ParkReasonSecurityScanRegressed) {
		t.Fatalf("unsafe private evidence lost durable parking reason: %v", err)
	}
	rows, err := th.store.ListDeploymentAudit(t.Context(), th.dep.ID, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("private lease refusal lost audit: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(rows[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if rows[0].Kind != state.DeployScanRegressed || data["reason"] != reason {
		t.Fatalf("wrong private lease failure reason: %v", data["reason"])
	}
}

type privateLeaseEvidenceTestStore struct {
	*state.MemStore
	failure error
}

func (s *privateLeaseEvidenceTestStore) GetFreshDeploymentRuntimeScan(ctx context.Context, account, app, dep string) (state.DeploymentRuntimeScanEvidence, error) {
	if s.failure != nil {
		return state.DeploymentRuntimeScanEvidence{}, s.failure
	}
	return s.MemStore.GetFreshDeploymentRuntimeScan(ctx, account, app, dep)
}

func TestPrivateSecurityLeasesBusyDefersAndStaleRefuses(t *testing.T) {
	loop, th := livePrivateLeaseFixture(t)
	store := &privateLeaseEvidenceTestStore{MemStore: th.store, failure: state.ErrApplicationStandardRuntimeBusy}
	loop.store = store
	before, err := th.store.GetCurrentDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	loop.reconcileSecurityLeases(t.Context(), time.Now(), time.Hour)
	store.failure = nil
	loop.reconcileSecurityLeases(t.Context(), time.Now(), time.Hour)
	app, err := th.store.AppByID(t.Context(), th.app.ID)
	if err != nil || app.Status != state.AppActive {
		t.Fatal("retryable contention quarantined runtime", err)
	}
	after, err := th.store.GetCurrentDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
	if err != nil || after.ID != before.ID || !after.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatal("lease read extended immutable evidence", err)
	}
	store.failure = state.ErrApplicationStandardRuntimeStale
	loop.reconcileSecurityLeases(t.Context(), time.Now(), time.Hour)
	assertPrivateLeaseQuarantine(t, th, "security_scan_evidence_invalid")
}

// Synthetic enrollment envelope verifies the consumer's fallback contract;
// real materialization/onboarding is covered by the state enrollment tests.
type managedLeaseTestStore struct{ *state.MemStore }

func (s managedLeaseTestStore) GetApplicationStandardEnrollment(context.Context, string, string) (state.ApplicationStandardEnrollment, error) {
	return state.ApplicationStandardEnrollment{MaterializedFields: []appstandards.Field{appstandards.SecurityPolicy}}, nil
}

func TestPrivateSecurityLeasesManagedAbsenceRefusesLegacy(t *testing.T) {
	store := managedLeaseTestStore{state.NewMemStore()}
	th := newTestHarness(t, state.DeploymentKindImage, api.PlanPro, "")
	store.MemStore = th.store
	app := th.app
	app.OrgID = uuid.NewString()
	loop := &Loop{store: store, log: silentLogger()}
	handled, reason, err := loop.privateSecurityLeaseFailure(t.Context(), app, th.dep)
	if err != nil || !handled || reason != "security_scan_evidence_missing" {
		t.Fatalf("managed absence fell back to legacy metadata: %v %q", err, reason)
	}
}
