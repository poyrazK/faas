package imaged

// adr: 429. Portable producer/lease/quarantine evidence, not native adoption.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/state"
)

func livePrivateLeaseFixture(t *testing.T, sidecar, base bool) (*Loop, *testHarness) {
	t.Helper()
	return livePrivateLeaseFixtureWithPartialScan(t, sidecar, base, false)
}

func livePrivateLeaseFixtureWithPartialScan(t *testing.T, sidecar, base, mainOnly bool) (*Loop, *testHarness) {
	t.Helper()
	h, th := producedScanFixtureWithBase(t, sidecar, base)
	if err := th.store.UpdateDeploymentStatus(t.Context(), th.dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
	if mainOnly {
		root, err := th.store.GetCurrentDeploymentRegistryRootfs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := h.runProducedComponentScan(t.Context(), th.app, th.dep, root, th.dep.ImageDigest); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := h.runDeployScan(t.Context(), th.app, th.dep); err != nil {
			t.Fatal(err)
		}
	}
	return &Loop{store: th.store, handler: h, log: silentLogger()}, th
}

func TestPrivateSecurityLeasesUseCompleteSetAndStorageClock(t *testing.T) {
	loop, th := livePrivateLeaseFixture(t, true, true)
	value, err := th.store.GetFreshDeploymentArtifactScanEvidence(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
	if err != nil || len(value.Components) != 2 || len(value.Bases) != 1 {
		t.Fatalf("actual producer fixture lost complete component set: %v", err)
	}
	if !value.ExpiresAt.Equal(value.Bases[0].ExpiresAt) || !value.ExpiresAt.Before(value.Components[0].ExpiresAt) {
		t.Fatal("older shared-base scan did not cap the complete evidence lease")
	}
	// The worker tick can be skewed; it cannot replace the storage-owned clock.
	loop.reconcileSecurityLeases(t.Context(), time.Now().Add(48*time.Hour), time.Hour)
	app, err := th.store.AppByID(t.Context(), th.app.ID)
	if err != nil || app.Status != state.AppActive {
		t.Fatalf("fresh storage evidence was quarantined by worker clock: %v", err)
	}
	rows, err := th.store.ListDeploymentAudit(t.Context(), th.dep.ID, 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("healthy private lease wrote a failure audit: %v", err)
	}
}

func publishUnsafePrivateComponent(t *testing.T, th *testHarness, base, failed bool) {
	t.Helper()
	if base {
		root, err := th.store.GetCurrentDeploymentRegistryRootfs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		value, err := th.store.GetFreshBaseImageScan(t.Context(), root.Input.BaseProducerID, root.Input.BaseInputHash)
		if err != nil {
			t.Fatal(err)
		}
		in := value.Input
		in.ID = uuid.NewString()
		if failed {
			in.Status, in.Failure, in.ScannerName, in.Report = "failed", "scanner_unavailable", "", nil
		} else {
			setPrivateLeaseHighFinding(in.Report)
		}
		if _, err := th.store.PublishBaseImageScan(t.Context(), in); err != nil {
			t.Fatal(err)
		}
		return
	}
	value, err := th.store.GetCurrentDeploymentArtifactScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "metrics")
	if err != nil {
		t.Fatal(err)
	}
	in := value.Input
	in.ID = uuid.NewString()
	if failed {
		in.Status, in.Failure, in.ScannerName, in.Report = "failed", "scanner_unavailable", "", nil
	} else {
		setPrivateLeaseHighFinding(in.Report)
	}
	if _, err := th.store.PublishDeploymentArtifactScan(t.Context(), in); err != nil {
		t.Fatal(err)
	}
}

func setPrivateLeaseHighFinding(report *api.ScanResult) {
	report.SeverityCounts = api.SeverityCounts{High: 1}
	report.Vulnerabilities = []api.Vulnerability{{ID: "CVE-fixture", Severity: "HIGH", Package: "fixture", Paths: []string{"/component/file"}}}
}

func TestPrivateSecurityLeasesRefuseUnsafeOrStaleComponents(t *testing.T) {
	for _, mode := range []string{"unsafe sidecar", "failed sidecar", "missing sidecar", "unsafe base", "failed base", "revoked signer", "metadata drift"} {
		t.Run(mode, func(t *testing.T) {
			loop, th := livePrivateLeaseFixtureWithPartialScan(t, true, true, mode == "missing sidecar")
			reason := "security_scan_evidence_invalid"
			switch mode {
			case "unsafe sidecar", "unsafe base":
				publishUnsafePrivateComponent(t, th, mode == "unsafe base", false)
				reason = "security_scan_regressed"
			case "failed sidecar", "failed base":
				publishUnsafePrivateComponent(t, th, mode == "failed base", true)
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
			dep, err := th.store.DeploymentByID(t.Context(), th.dep.ID)
			if err != nil || securityScanLeaseFailure(dep, time.Now().UTC(), time.Hour) != "" {
				t.Fatalf("fixture main compatibility scan should remain clean: %v", err)
			}
			loop.reconcileSecurityLeases(t.Context(), time.Now().UTC(), time.Hour)
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

func (s *privateLeaseEvidenceTestStore) GetFreshDeploymentArtifactScanEvidence(ctx context.Context, accountID, appID, depID string) (state.DeploymentArtifactScanEvidence, error) {
	if s.failure != nil {
		return state.DeploymentArtifactScanEvidence{}, s.failure
	}
	return s.MemStore.GetFreshDeploymentArtifactScanEvidence(ctx, accountID, appID, depID)
}

func TestPrivateSecurityLeasesBusyDefersAndStaleRefuses(t *testing.T) {
	loop, th := livePrivateLeaseFixture(t, true, false)
	store := &privateLeaseEvidenceTestStore{MemStore: th.store, failure: state.ErrApplicationStandardRuntimeBusy}
	loop.store = store
	before, err := th.store.GetCurrentDeploymentArtifactScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	loop.reconcileSecurityLeases(t.Context(), time.Now().UTC(), time.Hour)
	store.failure = nil
	loop.reconcileSecurityLeases(t.Context(), time.Now().UTC(), time.Hour)
	app, err := th.store.AppByID(t.Context(), th.app.ID)
	if err != nil || app.Status != state.AppActive {
		t.Fatalf("retryable contention quarantined app: %v", err)
	}
	after, err := th.store.GetCurrentDeploymentArtifactScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
	if err != nil || after.ID != before.ID || !after.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatalf("deferred lease read renewed immutable evidence: %v", err)
	}
	store.failure = state.ErrApplicationStandardRuntimeStale
	loop.reconcileSecurityLeases(t.Context(), time.Now().UTC(), time.Hour)
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
