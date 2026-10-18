package imaged

// adr: 435. Real private stores/pipeline with simulated native views and Grype.
// These are portable policy/worker tests, not ext4, Grype or KVM acceptance.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type pipelineRuntimeVMM struct {
	*nopVMMClient
	*fixtureRuntimeScanOwner
}

func pipelineRuntimeFixture(t *testing.T, sidecars bool) (*Handler, *testHarness, *pipelineRuntimeVMM) {
	t.Helper()
	h, th := producedScanFixtureWithBase(t, sidecars, true)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "package"), []byte("simulated guest packages"), 0600); err != nil {
		t.Fatal(err)
	}
	owner := &pipelineRuntimeVMM{nopVMMClient: &nopVMMClient{}, fixtureRuntimeScanOwner: &fixtureRuntimeScanOwner{source: source}}
	h.WithVMMClient(owner)
	h.runtimeScanParent = t.TempDir()
	h.WithRuntimeGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
	return h, th, owner
}

func publishComposedFixtureMutation(t *testing.T, th *testHarness, workload, status, severity string) state.DeploymentRuntimeScan {
	t.Helper()
	current, err := th.store.GetCurrentDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := current.Input
	in.ID = uuid.NewString()
	if status == "failed" {
		in.Status, in.Failure, in.Reports, in.Facts.Views = "failed", "scanner_unavailable", nil, nil
	} else {
		for i := range in.Reports {
			if in.Reports[i].WorkloadName != workload {
				continue
			}
			in.Reports[i].Report.Vulnerabilities = []api.Vulnerability{{ID: "CVE-composed-fixture", Severity: severity}}
			switch severity {
			case "HIGH":
				in.Reports[i].Report.SeverityCounts = api.SeverityCounts{High: 1}
			case "UNKNOWN":
				in.Reports[i].Report.SeverityCounts = api.SeverityCounts{Unknown: 1}
			}
		}
	}
	value, err := th.store.PublishDeploymentRuntimeScan(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestProducedRuntimePipelineAutomaticallyPublishesAndIgnoresComponentFindings(t *testing.T) {
	h, th, owner := pipelineRuntimeFixture(t, true)
	h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
	if err := collectProducedFixtureScans(t, h, th); err != nil {
		t.Fatal(err)
	}
	root, err := th.store.GetCurrentDeploymentRegistryRootfs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	base, err := th.store.GetFreshBaseImageScan(t.Context(), root.Input.BaseProducerID, root.Input.BaseInputHash)
	if err != nil {
		t.Fatal(err)
	}
	base.Input.ID = uuid.NewString()
	setPrivateLeaseHighFinding(base.Input.Report)
	if _, err := th.store.PublishBaseImageScan(t.Context(), base.Input); err != nil {
		t.Fatal(err)
	}
	component, err := th.store.GetCurrentDeploymentArtifactScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	component.Input.ID = uuid.NewString()
	setPrivateLeaseHighFinding(component.Input.Report)
	if _, err := th.store.PublishDeploymentArtifactScan(t.Context(), component.Input); err != nil {
		t.Fatal(err)
	}
	h.WithGrypeRun(func(context.Context, string) (*ScanResult, error) {
		t.Fatal("deployment used component extraction instead of native views")
		return nil, nil
	})
	if err := h.runDeployScan(t.Context(), th.app, th.dep); err != nil {
		t.Fatal("clean composed runtime blocked by component findings", err)
	}
	current, err := th.store.GetFreshDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
	if err != nil || len(current.Scan.Input.Reports) != 2 || len(current.Scan.Input.Artifacts) != 3 || owner.calls != 1 {
		t.Fatal("automatic pipeline lost composed membership", err)
	}
	if err := th.store.UpdateDeploymentStatus(t.Context(), th.dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	l := &Loop{store: th.store, handler: h, log: silentLogger()}
	l.reconcileSecurityLeases(t.Context(), time.Now().Add(48*time.Hour), time.Hour)
	l.rescanLiveDeployment(t.Context(), th.app, th.dep)
	app, err := th.store.AppByID(t.Context(), th.app.ID)
	if err != nil || app.Status != state.AppActive {
		t.Fatal("component/legacy finding quarantined composed-clean service", err)
	}
}

func TestProducedRuntimePipelineUsesEveryViewForPolicy(t *testing.T) {
	for _, policy := range []api.AppSecurityPolicy{api.AppSecurityPolicyEnforce, api.AppSecurityPolicyWarn, api.AppSecurityPolicyOff} {
		for _, workload := range []string{"main", "metrics"} {
			t.Run(string(policy)+"/"+workload, func(t *testing.T) {
				h, th, owner := pipelineRuntimeFixture(t, true)
				th.app.SecurityPolicy = policy
				calls := 0
				h.WithRuntimeGrypeRun(func(ctx context.Context, dir string) (*ScanResult, error) {
					calls++
					return producedScanResult(t, filepath.Base(dir) == map[string]string{"main": "main", "metrics": "sidecar-metrics"}[workload]), nil
				})
				err := h.runDeployScan(t.Context(), th.app, th.dep)
				if (err != nil) != (policy == api.AppSecurityPolicyEnforce) || err != nil && !errors.Is(err, errSecurityScanBlocked) {
					t.Fatal("composed findings policy mismatch", err)
				}
				value, readErr := th.store.GetFreshDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
				if readErr != nil || calls != 2 || owner.calls != 1 || privateSecurityScanFailure(value) != "security_scan_regressed" {
					t.Fatal("blocking findings were discarded or a view skipped", readErr)
				}
			})
		}
	}
}

func TestProducedRuntimePipelineFailurePublishesClosedSelectionAndSafeDetail(t *testing.T) {
	for _, policy := range []api.AppSecurityPolicy{api.AppSecurityPolicyEnforce, api.AppSecurityPolicyWarn, api.AppSecurityPolicyOff} {
		t.Run(string(policy), func(t *testing.T) {
			h, th, _ := pipelineRuntimeFixture(t, true)
			if err := h.runDeployScan(t.Context(), th.app, th.dep); err != nil {
				t.Fatal(err)
			}
			previous, err := th.store.GetCurrentDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			th.app.SecurityPolicy = policy
			h.WithRuntimeGrypeRun(func(context.Context, string) (*ScanResult, error) {
				return nil, errors.New("sensitive scanner credential=fixture-secret")
			})
			err = h.runDeployScan(t.Context(), th.app, th.dep)
			if !errors.Is(err, errSecurityScanBlocked) || strings.Contains(err.Error(), "fixture-secret") {
				t.Fatal("failed composed evidence permitted deployment or exposed scanner detail", err)
			}
			current, err := th.store.GetCurrentDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
			if err != nil || current.ID == previous.ID || current.Input.Status != "failed" || len(current.Input.Reports) != 0 {
				t.Fatal("failed runtime rescan retained prior success", err)
			}
			if _, err := th.store.GetFreshDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID); !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
				t.Fatal("failed selection acquired authority", err)
			}
		})
	}
}

func TestProducedRuntimeRenewalUsesDurableScanLeaseAndRecoversAfterRestart(t *testing.T) {
	h, th, owner := pipelineRuntimeFixture(t, true)
	if err := th.store.UpdateDeploymentStatus(t.Context(), th.dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	l := &Loop{store: th.store, handler: h, log: silentLogger()}
	l.reconcileProducedSecurityScans(t.Context(), time.Now())
	first, err := th.store.GetCurrentDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
	if err != nil || owner.calls != 1 {
		t.Fatal("startup did not reconstruct pending composed scan", err)
	}
	l.reconcileProducedSecurityScans(t.Context(), first.ScannedAt.Add(time.Second))
	if owner.calls != 1 {
		t.Fatal("fresh scan inherited legacy six-hour scheduling")
	}
	publishComposedFixtureMutation(t, th, "", "failed", "")
	l = &Loop{store: th.store, handler: h, log: silentLogger()}
	l.reconcileProducedSecurityScans(t.Context(), time.Now())
	if owner.calls != 2 {
		t.Fatal("restarted worker did not retry failed durable selection")
	}
	l.reconcileProducedSecurityScans(t.Context(), time.Now().Add(api.ApplicationStandardArtifactScanRenewEvery))
	if owner.calls != 3 {
		t.Fatal("private scan lease was not renewed before expiry")
	}
	root, err := th.store.GetCurrentDeploymentRegistryRootfs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	approval, err := th.store.GetLatestDeploymentRegistryVerification(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := th.store.UpdateDeploymentStatus(t.Context(), th.dep.ID, state.DeployImaging, ""); err != nil {
		t.Fatal(err)
	}
	root.Input.ID, root.Input.RegistryVerificationID, root.Input.RegistryInputHash = uuid.NewString(), approval.ID, approval.InputHash
	if _, err := th.store.PublishDeploymentRegistryRootfs(t.Context(), root.Input); err != nil {
		t.Fatal(err)
	}
	if err := th.store.UpdateDeploymentStatus(t.Context(), th.dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	l.reconcileProducedSecurityScans(t.Context(), time.Now())
	last, err := th.store.GetFreshDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
	if err != nil || owner.calls != 4 || last.Scan.Input.Facts.InputHash == first.Input.Facts.InputHash {
		t.Fatal("producer replacement reused an earlier composed scan", err)
	}
}

func TestProducedRuntimePublicationDefersDuplicateJobAndReleasesOnCompletion(t *testing.T) {
	h, th, owner := pipelineRuntimeFixture(t, true)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	h.WithRuntimeGrypeRun(func(ctx context.Context, _ string) (*ScanResult, error) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return producedScanResult(t, false), nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := h.ScanAndPublishProducedRuntime(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatal("scan stopped before entering", err)
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not enter")
	}
	_, err := h.ScanAndPublishProducedRuntime(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
	if !producedEvidenceBusy(err) {
		t.Fatal("duplicate runtime job entered scanner", err)
	}
	if _, err := th.store.GetCurrentDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("busy job fabricated failure", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if owner.calls != 1 || calls.Load() != 2 {
		t.Fatal("duplicate job materialized extra views")
	}
	if _, err := h.ScanAndPublishProducedRuntime(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID); err != nil {
		t.Fatal("job remained fenced after completion", err)
	}
	h.runtimeScanMu.Lock()
	active := len(h.runtimeScanActive)
	h.runtimeScanMu.Unlock()
	if active != 0 {
		t.Fatal("completed job retained coordination entry")
	}
}

type busyRuntimeRenewalStore struct {
	*state.MemStore
	point        string
	failedWrites int
}

func (s *busyRuntimeRenewalStore) RecordDeploymentRegistryVerification(ctx context.Context, in state.DeploymentRegistryVerificationInput) (state.DeploymentRegistryVerification, error) {
	if s.point == "signature" {
		return state.DeploymentRegistryVerification{}, state.ErrApplicationStandardRuntimeBusy
	}
	return s.MemStore.RecordDeploymentRegistryVerification(ctx, in)
}
func (s *busyRuntimeRenewalStore) GetFreshDeploymentRuntimeProducerInputs(ctx context.Context, account, app, dep string) (state.DeploymentRuntimeProducerInputs, error) {
	if s.point == "inputs" {
		return state.DeploymentRuntimeProducerInputs{}, state.ErrApplicationStandardRuntimeBusy
	}
	return s.MemStore.GetFreshDeploymentRuntimeProducerInputs(ctx, account, app, dep)
}
func (s *busyRuntimeRenewalStore) PublishDeploymentRuntimeScan(ctx context.Context, in state.DeploymentRuntimeScanInput) (state.DeploymentRuntimeScan, error) {
	if in.Status == "failed" {
		s.failedWrites++
	}
	if s.point == "publication" {
		return state.DeploymentRuntimeScan{}, state.ErrApplicationStandardRuntimeBusy
	}
	return s.MemStore.PublishDeploymentRuntimeScan(ctx, in)
}

func TestProducedRuntimeRenewalBusyRefusesAdmissionWithoutFailureOrQuarantine(t *testing.T) {
	for _, point := range []string{"signature", "inputs", "publication"} {
		t.Run(point, func(t *testing.T) {
			h, th, _ := pipelineRuntimeFixture(t, true)
			if err := h.runDeployScan(t.Context(), th.app, th.dep); err != nil {
				t.Fatal(err)
			}
			if err := th.store.UpdateDeploymentStatus(t.Context(), th.dep.ID, state.DeployLive, ""); err != nil {
				t.Fatal(err)
			}
			before, err := th.store.GetCurrentDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			store := &busyRuntimeRenewalStore{MemStore: th.store, point: point}
			h.store = store
			if err := h.runDeployScan(t.Context(), th.app, th.dep); !producedEvidenceBusy(err) {
				t.Fatal("contention permitted admission", err)
			}
			l := &Loop{store: store, handler: h, log: silentLogger()}
			l.rescanLiveDeployment(t.Context(), th.app, th.dep)
			current, err := th.store.GetCurrentDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
			app, readErr := th.store.AppByID(t.Context(), th.app.ID)
			if err != nil || readErr != nil || app.Status != state.AppActive || current.ID != before.ID || !current.ExpiresAt.Equal(before.ExpiresAt) || store.failedWrites != 0 {
				t.Fatal("busy renewal changed facts or quarantined", err, readErr)
			}
			store.point = ""
			l.rescanLiveDeployment(t.Context(), th.app, th.dep)
			current, err = th.store.GetCurrentDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
			if err != nil || current.ID == before.ID || current.Input.Status != "complete" {
				t.Fatal("worker did not recover after contention", err)
			}
		})
	}
}

func TestProducedRuntimePublicScanJobUsesConfiguredOwner(t *testing.T) {
	h, th, _ := pipelineRuntimeFixture(t, true)
	if _, err := h.ScanProducedRuntime(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID); err != nil {
		t.Fatalf("public scan method: %T %v", err, err)
	}
	if _, err := h.ScanAndPublishProducedRuntime(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID); err != nil {
		t.Fatalf("public publication method: %T %v", err, err)
	}
}

func TestProducedRuntimeScopeAcceptsUUIDSpellingsAndRejectsOtherOwner(t *testing.T) {
	_, th, _ := pipelineRuntimeFixture(t, true)
	in, err := th.store.GetFreshDeploymentRuntimeProducerInputs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !runtimeScanScopeMatches(in, th.app.AccountID, th.app.ID, th.dep.ID) || !runtimeScanScopeMatches(in, in.AccountID, in.AppID, in.DeploymentID) {
		t.Fatal("equivalent UUID spelling refused")
	}
	for i := range 3 {
		ids := []string{th.app.AccountID, th.app.ID, th.dep.ID}
		ids[i] = uuid.NewString()
		if runtimeScanScopeMatches(in, ids[0], ids[1], ids[2]) {
			t.Fatal("different owner scope accepted", i)
		}
	}
	if runtimeScanScopeMatches(in, "invalid", th.app.ID, th.dep.ID) {
		t.Fatal("malformed scope accepted")
	}
}

func TestProducedRuntimePresenceNeverFallsBackAfterMainMetadataDrift(t *testing.T) {
	h, th, _ := pipelineRuntimeFixture(t, false)
	if err := h.runDeployScan(t.Context(), th.app, th.dep); err != nil {
		t.Fatal(err)
	}
	if err := th.store.UpdateDeploymentStatus(t.Context(), th.dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	if err := th.store.SetDeploymentRootfs(t.Context(), th.dep.ID, "/changed.ext4", "apps/changed.ext4", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := th.store.GetCurrentDeploymentRegistryRootfs(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID, ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("fixture did not hide validated current producer", err)
	}
	if err := h.runDeployScan(t.Context(), th.app, th.dep); !errors.Is(err, errSecurityScanBlocked) {
		t.Fatal("retained producer metadata drift fell back to legacy scan", err)
	}
	l := &Loop{store: th.store, handler: h, log: silentLogger()}
	l.reconcileSecurityLeases(t.Context(), time.Now(), time.Hour)
	assertPrivateLeaseQuarantine(t, th, "security_scan_evidence_invalid")
}

func TestProducedRuntimeRenewalRefreshesPendingSnapshotWithoutQuarantiningServingApp(t *testing.T) {
	h, th, owner := pipelineRuntimeFixture(t, true)
	if err := th.store.UpdateDeploymentStatus(t.Context(), th.dep.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	l := &Loop{store: th.store, handler: h, log: silentLogger()}
	l.reconcileProducedSecurityScans(t.Context(), time.Now())
	first, err := th.store.GetCurrentDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
	if err != nil || owner.calls != 1 {
		t.Fatal("pending snapshot did not obtain initial composed facts", err)
	}
	l.reconcileProducedSecurityScans(t.Context(), time.Now().Add(api.ApplicationStandardArtifactScanRenewEvery))
	current, err := th.store.GetCurrentDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
	if err != nil || current.ID == first.ID || owner.calls != 2 {
		t.Fatal("pending release/prime inherited expired scan", err)
	}
	h.WithRuntimeGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, true), nil })
	l.reconcileProducedSecurityScans(t.Context(), time.Now().Add(2*api.ApplicationStandardArtifactScanRenewEvery))
	current, err = th.store.GetCurrentDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID)
	if err != nil || current.Input.Reports[0].Report.SeverityCounts.High != 1 {
		t.Fatal("pending scan lost blocking findings", err)
	}
	app, appErr := th.store.AppByID(t.Context(), th.app.ID)
	dep, depErr := th.store.DeploymentByID(t.Context(), th.dep.ID)
	if appErr != nil || depErr != nil || app.Status != state.AppActive || dep.Status != state.DeploySnapshotting || dep.ParkedReason != "" {
		t.Fatal("pending evidence renewal quarantined serving app or advanced rollout", appErr, depErr)
	}
}

func TestProducedRuntimePublicationCancellationReleasesJobWithoutSelectingFailure(t *testing.T) {
	h, th, _ := pipelineRuntimeFixture(t, true)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	h.WithRuntimeGrypeRun(func(ctx context.Context, _ string) (*ScanResult, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	go func() {
		_, err := h.ScanAndPublishProducedRuntime(ctx, th.app.AccountID, th.app.ID, th.dep.ID)
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatal("scan ended before cancellation", err)
	case <-time.After(5 * time.Second):
		t.Fatal("scanner did not enter")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost classification", err)
	}
	if _, err := th.store.GetCurrentDeploymentRuntimeScan(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cancelled job selected scan facts", err)
	}
	h.WithRuntimeGrypeRun(func(context.Context, string) (*ScanResult, error) { return producedScanResult(t, false), nil })
	if _, err := h.ScanAndPublishProducedRuntime(t.Context(), th.app.AccountID, th.app.ID, th.dep.ID); err != nil {
		t.Fatal("cancelled job retained coordination fence", err)
	}
}
