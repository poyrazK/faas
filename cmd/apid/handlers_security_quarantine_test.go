package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRecoverAppSecurityQuarantineRequiresCleanReplacement(t *testing.T) {
	e, _ := newTestServerWithCapturingNotifier(t, api.PlanPro)
	old := mustSeedDeployment(t, e, "recover-blocked")
	if err := e.store.MarkDeploymentLive(t.Context(), old.ID); err != nil {
		t.Fatal(err)
	}
	parkedAt := time.Now().UTC().Add(-time.Minute)
	if err := e.store.SetDeploymentParked(t.Context(), old.ID, string(state.ParkReasonSecurityScanRegressed), parkedAt); err != nil {
		t.Fatal(err)
	}
	parked := state.AppEvictedCold
	if _, err := e.store.UpdateApp(t.Context(), old.AppID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatal(err)
	}
	replacement, err := e.store.CreateDeployment(t.Context(), state.Deployment{
		AppID: old.AppID, ImageDigest: "sha256:replacement", Kind: state.DeploymentKindImage,
		Status: state.DeployPending, CreatedAt: parkedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(t.Context(), replacement.ID); err != nil {
		t.Fatal(err)
	}
	stampSecurityScan(t, e, replacement, replacement.ImageDigest, 0, 1, 0)

	rec := e.do(t, http.MethodPost, "/v1/apps/recover-blocked/security/recover",
		api.SecurityQuarantineRecoveryRequest{DeploymentID: replacement.ID},
		map[string]string{"Idempotency-Key": "recover-blocked"})
	assertProblem(t, rec, http.StatusConflict, api.CodeSecurityQuarantineRecoveryBlocked)
	app, err := e.store.AppByID(t.Context(), old.AppID)
	if err != nil {
		t.Fatal(err)
	}
	if app.Status != state.AppEvictedCold {
		t.Fatalf("app status = %q, want evicted_cold", app.Status)
	}
}

func TestRecoverAppSecurityQuarantineRestoresCleanReplacement(t *testing.T) {
	e, notif := newTestServerWithCapturingNotifier(t, api.PlanPro)
	old := mustSeedDeployment(t, e, "recover-clean")
	if err := e.store.MarkDeploymentLive(t.Context(), old.ID); err != nil {
		t.Fatal(err)
	}
	parkedAt := time.Now().UTC().Add(-time.Minute)
	if err := e.store.SetDeploymentParked(t.Context(), old.ID, string(state.ParkReasonSecurityScanRegressed), parkedAt); err != nil {
		t.Fatal(err)
	}
	parked := state.AppEvictedCold
	if _, err := e.store.UpdateApp(t.Context(), old.AppID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatal(err)
	}
	replacement, err := e.store.CreateDeployment(t.Context(), state.Deployment{
		AppID: old.AppID, ImageDigest: "sha256:clean-replacement", Kind: state.DeploymentKindImage,
		Status: state.DeployPending, CreatedAt: parkedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(t.Context(), replacement.ID); err != nil {
		t.Fatal(err)
	}
	stampSecurityScan(t, e, replacement, replacement.ImageDigest, 0, 0, 0)

	rec := e.do(t, http.MethodPost, "/v1/apps/recover-clean/security/recover",
		api.SecurityQuarantineRecoveryRequest{DeploymentID: replacement.ID},
		map[string]string{"Idempotency-Key": "recover-clean"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var response api.SecurityQuarantineRecoveryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.DeploymentID != replacement.ID || response.ImageDigest != replacement.ImageDigest || response.Status != string(state.AppActive) {
		t.Fatalf("recovery response = %+v", response)
	}
	app, err := e.store.AppByID(t.Context(), old.AppID)
	if err != nil {
		t.Fatal(err)
	}
	if app.Status != state.AppActive {
		t.Fatalf("app status = %q, want active", app.Status)
	}
	notif.mu.Lock()
	defer notif.mu.Unlock()
	var recovered bool
	for _, emitted := range notif.emitted {
		if emitted.Channel != db.NotifyAppChanged || !strings.Contains(emitted.Payload, `"kind":"security_recovered"`) {
			continue
		}
		recovered = true
		if !strings.Contains(emitted.Payload, replacement.ID) || !strings.Contains(emitted.Payload, `"lifecycle_changed":true`) {
			t.Fatalf("recovery notification = %s, missing replacement id", emitted.Payload)
		}
	}
	if !recovered {
		t.Fatalf("missing security recovery notification: %+v", notif.emitted)
	}
}

func TestGetAppSecurityIncludesActiveQuarantine(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "posture-quarantine")
	if err := e.store.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
		t.Fatal(err)
	}
	parkedAt := time.Now().UTC().Add(-time.Minute)
	if err := e.store.SetDeploymentParked(t.Context(), dep.ID, string(state.ParkReasonSecurityScanRegressed), parkedAt); err != nil {
		t.Fatal(err)
	}
	parked := state.AppEvictedCold
	if _, err := e.store.UpdateApp(t.Context(), dep.AppID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, http.MethodGet, "/v1/apps/posture-quarantine/security", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var posture api.AppSecurityPostureResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &posture); err != nil {
		t.Fatal(err)
	}
	if posture.Quarantine == nil || posture.Quarantine.DeploymentID != dep.ID || posture.Quarantine.Reason != string(state.ParkReasonSecurityScanRegressed) {
		t.Fatalf("quarantine posture = %+v", posture.Quarantine)
	}
}

func TestGetAppSecurityReportsLiveImageScanCoverage(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "posture-scan-coverage")
	if err := e.store.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
		t.Fatal(err)
	}
	setSecurityPolicyForTest(t, e, "posture-scan-coverage", api.AppSecurityPolicyEnforce)

	readFindings := func() map[string]api.AppSecurityFinding {
		t.Helper()
		rec := e.do(t, http.MethodGet, "/v1/apps/posture-scan-coverage/security", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		var posture api.AppSecurityPostureResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &posture); err != nil {
			t.Fatal(err)
		}
		out := make(map[string]api.AppSecurityFinding, len(posture.Findings))
		for _, finding := range posture.Findings {
			out[finding.Code] = finding
		}
		return out
	}

	findings := readFindings()
	if finding, ok := findings["image_scan_missing"]; !ok || finding.Severity != "high" {
		t.Fatalf("missing scan finding = %+v, want high severity", findings["image_scan_missing"])
	}

	stampSecurityScan(t, e, dep, dep.ImageDigest, 0, 1, 0)
	findings = readFindings()
	if finding, ok := findings["image_scan_blocking"]; !ok || finding.Severity != "high" {
		t.Fatalf("blocking scan finding = %+v, want high severity", findings["image_scan_blocking"])
	}

	stampSecurityScan(t, e, dep, dep.ImageDigest, 0, 0, 0)
	findings = readFindings()
	if _, ok := findings["image_scan_missing"]; ok {
		t.Fatalf("missing scan finding remained after clean evidence: %+v", findings["image_scan_missing"])
	}
	if _, ok := findings["image_scan_blocking"]; ok {
		t.Fatalf("blocking scan finding remained after clean evidence: %+v", findings["image_scan_blocking"])
	}

	setSecurityPolicyForTest(t, e, "posture-scan-coverage", api.AppSecurityPolicyWarn)
	stampSecurityScan(t, e, dep, dep.ImageDigest, 0, 1, 0)
	findings = readFindings()
	if finding, ok := findings["image_scan_blocking"]; !ok || finding.Severity != "medium" {
		t.Fatalf("warn-mode blocking scan finding = %+v, want medium severity", findings["image_scan_blocking"])
	}
}

func TestGetAppSecurityDoesNotRequireImportedImageScanForSourceDeployment(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "posture-source-coverage")
	dep, err := e.store.CreateDeployment(t.Context(), state.Deployment{
		AppID: appID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentRootfs(t.Context(), dep.ID, "/srv/fc/apps/posture-source-coverage/"+dep.ID+".ext4", "apps/posture-source-coverage/"+dep.ID+".ext4", 1); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(t.Context(), dep.ID); err != nil {
		t.Fatal(err)
	}
	setSecurityPolicyForTest(t, e, "posture-source-coverage", api.AppSecurityPolicyEnforce)
	rec := e.do(t, http.MethodGet, "/v1/apps/posture-source-coverage/security", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var posture api.AppSecurityPostureResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &posture); err != nil {
		t.Fatal(err)
	}
	for _, finding := range posture.Findings {
		if strings.HasPrefix(finding.Code, "image_scan_") {
			t.Fatalf("source deployment has imported-image finding: %+v", finding)
		}
	}
}

func stampSecurityScan(t *testing.T, e testEnv, dep state.Deployment, digest string, critical, high, unknown int) {
	t.Helper()
	now := time.Now().UTC()
	payload, err := json.Marshal(api.ScanResult{
		ScannedAt: now.Format(time.RFC3339Nano), ImageDigest: digest,
		ArtifactDigest: "sha256:artifact", ScannerVersion: "grype-test",
		ScannerDBStatus: "valid", ScannerDBVersion: "db-test",
		ScannerDBBuiltAt: now.Add(-time.Hour).Format(time.RFC3339Nano),
		SeverityCounts:   api.SeverityCounts{Critical: critical, High: high, Unknown: unknown},
		Vulnerabilities:  []api.Vulnerability{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpsertDeploymentScanResult(context.Background(), dep.ID, payload, "complete"); err != nil {
		t.Fatal(err)
	}
}
