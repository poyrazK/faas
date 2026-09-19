package imaged

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSecurityScanDue(t *testing.T) {
	now := time.Date(2026, 9, 19, 15, 0, 0, 0, time.UTC)
	if !securityScanDue(state.Deployment{}, now, time.Hour) {
		t.Fatal("deployment without scan evidence should be due")
	}
	dep := state.Deployment{ScannedAt: now.Add(-time.Hour)}
	if !securityScanDue(dep, now, time.Hour) {
		t.Fatal("deployment at the re-scan boundary should be due")
	}
	dep.ScannedAt = now.Add(-time.Minute)
	if securityScanDue(dep, now, time.Hour) {
		t.Fatal("fresh scan evidence should not be due")
	}
}

func TestSecurityScanRegressionOnlyEmitsOnCleanToUnsafe(t *testing.T) {
	clean := &ScanResult{SeverityCounts: SeverityCounts{Medium: 1}}
	unsafe := &ScanResult{SeverityCounts: SeverityCounts{Critical: 1}}
	if !securityScanRegression("complete", clean, "complete", unsafe) {
		t.Fatal("clean to critical should be a regression")
	}
	if securityScanRegression("complete", unsafe, "complete", unsafe) {
		t.Fatal("repeated blocking evidence should not emit another regression")
	}
	if securityScanRegression("complete", clean, "complete", clean) {
		t.Fatal("clean evidence should not be a regression")
	}
	if !securityScanRegression("", nil, "failed", &ScanResult{Error: "grype unavailable"}) {
		t.Fatal("missing evidence to failed re-scan should require quarantine")
	}
}

func TestAppendSecurityScanRegressionAudit(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(context.Background(), "security-rescan@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID:      account.ID,
		Slug:           "rescan-app",
		SecurityPolicy: api.AppSecurityPolicyEnforce,
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:rescan",
		Status:      state.DeployLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	loop := &Loop{store: store}
	current := &ScanResult{
		SeverityCounts:   SeverityCounts{Critical: 1},
		ScannerDBVersion: "2026-09-19",
		ScannerDBBuiltAt: "2026-09-19T14:00:00Z",
	}
	if err := loop.appendSecurityScanRegression(context.Background(), app, dep, "complete", &ScanResult{}, "complete", current); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListDeploymentAudit(context.Background(), dep.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Kind != state.DeployScanRegressed {
		t.Fatalf("audit rows = %+v, want one scan regression", rows)
	}
	var data map[string]any
	if err := json.Unmarshal(rows[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["quarantine_required"] != true || data["image_digest"] != dep.ImageDigest {
		t.Fatalf("audit data = %v, want quarantine signal for %s", data, dep.ImageDigest)
	}
}
