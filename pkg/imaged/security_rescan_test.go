package imaged

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
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

func TestSecurityScanLeaseFailure(t *testing.T) {
	now := time.Date(2026, 9, 19, 15, 0, 0, 0, time.UTC)
	base := ScanResult{
		ImageDigest:      "sha256:lease",
		ArtifactDigest:   "sha256:artifact",
		ScannedAt:        now.Add(-time.Hour).Format(time.RFC3339Nano),
		ScannerVersion:   "0.78.0",
		ScannerDBStatus:  "valid",
		ScannerDBVersion: "2026-09-19",
		ScannerDBBuiltAt: now.Add(-time.Hour).Format(time.RFC3339Nano),
	}
	encode := func(result ScanResult) []byte {
		raw, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	dep := state.Deployment{ImageDigest: "sha256:lease", ScanStatus: "complete", ScanResult: encode(base)}
	if got := securityScanLeaseFailure(dep, now, 2*time.Hour); got != "" {
		t.Fatalf("fresh lease failure = %q, want none", got)
	}
	dep.ScanResult = encode(func() ScanResult {
		result := base
		result.ImageDigest = "sha256:other"
		return result
	}())
	if got := securityScanLeaseFailure(dep, now, 2*time.Hour); got != "security_scan_digest_drift" {
		t.Fatalf("digest drift reason = %q", got)
	}
	dep.ScanResult = encode(func() ScanResult {
		result := base
		result.ScannedAt = now.Add(-3 * time.Hour).Format(time.RFC3339Nano)
		return result
	}())
	if got := securityScanLeaseFailure(dep, now, 2*time.Hour); got != "security_scan_evidence_expired" {
		t.Fatalf("expired reason = %q", got)
	}
}

func TestReconcileSecurityLeasesQuarantinesExpiredEvidence(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(context.Background(), "security-lease@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID:      account.ID,
		Slug:           "lease-app",
		Status:         state.AppActive,
		SecurityPolicy: api.AppSecurityPolicyEnforce,
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:lease",
		Status:      state.DeployLive,
		ScanStatus:  "complete",
		ScannedAt:   time.Now().UTC().Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	notif := &fakeNotifier{}
	loop := &Loop{
		store: store,
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		handler: &Handler{
			store: store,
			notif: notif,
		},
	}
	now := time.Date(2026, 9, 19, 15, 0, 0, 0, time.UTC)
	loop.reconcileSecurityLeases(context.Background(), now, time.Hour)
	gotApp, err := store.AppByID(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotApp.Status != state.AppEvictedCold {
		t.Fatalf("app status = %q, want evicted_cold", gotApp.Status)
	}
	rows, err := store.ListDeploymentAudit(context.Background(), dep.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Kind != state.DeployScanRegressed {
		t.Fatalf("audit rows = %+v, want one lease quarantine", rows)
	}
	var data map[string]any
	if err := json.Unmarshal(rows[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["reason"] != "security_scan_evidence_missing" {
		t.Fatalf("lease audit reason = %v", data["reason"])
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

func TestQuarantineSecurityRegressionParksAndNotifies(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(context.Background(), "security-quarantine@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: account.ID,
		Slug:      "quarantine-app",
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:quarantine",
		Status:      state.DeployLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	notif := &fakeNotifier{}
	loop := &Loop{
		store: store,
		handler: &Handler{
			store: store,
			notif: notif,
		},
		now: func() time.Time { return time.Date(2026, 9, 19, 15, 0, 0, 0, time.UTC) },
	}
	if err := loop.quarantineSecurityRegression(context.Background(), app, dep); err != nil {
		t.Fatal(err)
	}
	gotApp, err := store.AppByID(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotApp.Status != state.AppEvictedCold {
		t.Fatalf("app status = %q, want evicted_cold", gotApp.Status)
	}
	gotDep, err := store.DeploymentByID(context.Background(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotDep.ParkedReason != string(state.ParkReasonSecurityScanRegressed) {
		t.Fatalf("parked reason = %q, want security_scan_regressed", gotDep.ParkedReason)
	}
	call := findNotify(notif, db.NotifyAppChanged)
	if call == nil {
		t.Fatal("security quarantine did not notify app_changed")
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(call.payload), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["kind"] != "parked" || payload["reason"] != string(state.ParkReasonSecurityScanRegressed) {
		t.Fatalf("notification payload = %v, want security parked event", payload)
	}

	// A retry is idempotent and preserves the first parked timestamp.
	if err := loop.quarantineSecurityRegression(context.Background(), gotApp, gotDep); err != nil {
		t.Fatal(err)
	}
	if len(notif.calls) != 2 {
		t.Fatalf("notification calls = %d, want one per durable retry", len(notif.calls))
	}
}
