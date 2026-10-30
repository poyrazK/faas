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
	dep.ScannedAt = now.Add(-55 * time.Minute)
	if !securityScanDue(dep, now, time.Hour) {
		t.Fatal("evidence inside the renew margin should be due")
	}
}

// The live sweep must check evidence before the enforce lease runs out,
// whatever the sweep's phase and however long the scan takes up to a
// generous bound. With due == every, a tick landing just short of the mark
// skipped the deployment and the lease expired first (bug hunt 2026-10-28
// B34).
func TestSecurityScanSweepChecksEvidenceBeforeTheLeaseExpires(t *testing.T) {
	every := securityScanSweepEvery
	check := securityScanCheckEvery(every)
	lease := every + securityScanLeaseSweepEvery
	start := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	for phase := time.Duration(0); phase < check; phase += time.Minute {
		for _, scanTook := range []time.Duration{0, 3 * time.Minute, 30 * time.Minute} {
			checked := start
			for tick := start.Add(phase); tick.Before(start.Add(72 * time.Hour)); tick = tick.Add(check) {
				if !securityScanDue(state.Deployment{ScannedAt: checked}, tick, every) {
					continue
				}
				done := tick.Add(scanTook)
				if done.Sub(checked) > lease {
					t.Fatalf("phase %s, scan %s: evidence from %s renewed at %s, after its %s lease", phase, scanTook, checked, done, lease)
				}
				checked = done
			}
		}
	}
	if check > securityScanRenewMargin(every)/2 {
		t.Fatalf("sweep every %s leaves too little of the %s margin", check, securityScanRenewMargin(every))
	}
}

// An enforce app whose image and scanner database stay unchanged for a day
// must stay live. #5776's live rescan skipped current evidence without
// writing anything, so the lease parked the app six hours after its last
// real scan (bug hunt 2026-10-28 B33).
func TestReusedLiveEvidenceKeepsAnEnforceAppServing(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "renew-evidence@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "renew-app", Status: state.AppActive, SecurityPolicy: api.AppSecurityPolicyEnforce})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	built := start.Add(-2 * time.Hour).Format(time.RFC3339Nano)
	scannedClock := start.Add(-5*time.Hour - 30*time.Minute)
	scannedAt := scannedClock.Format(time.RFC3339Nano)
	evidence, err := json.Marshal(ScanResult{
		ImageDigest: "sha256:renew", ArtifactDigest: "sha256:artifact", ScannedAt: scannedAt,
		ScannerVersion: "0.116.0", ScannerDBStatus: "valid", ScannerDBVersion: "v6.1.10", ScannerDBBuiltAt: built,
		SeverityCounts: SeverityCounts{Low: 1}, Vulnerabilities: []Vulnerability{},
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:renew", Status: state.DeployLive,
		ScanStatus: "complete", ScanResult: evidence, ScannedAt: scannedClock,
	})
	if err != nil {
		t.Fatal(err)
	}
	clock := start
	loop := &Loop{
		store: store,
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:   func() time.Time { return clock },
		handler: (&Handler{store: store, notif: &fakeNotifier{}}).WithScannerIdentity(func(context.Context) (installedScanner, error) {
			return installedScanner{Version: "0.116.0", DBVersion: "v6.1.10", DBBuiltAt: built}, nil
		}),
	}
	check := securityScanCheckEvery(securityScanSweepEvery)
	for clock.Before(start.Add(24 * time.Hour)) {
		if clock.Sub(start)%check == 0 {
			loop.reconcileSecurityScans(ctx, clock, securityScanSweepEvery)
		}
		loop.reconcileSecurityLeases(ctx, clock, securityScanSweepEvery)
		got, err := store.AppByID(ctx, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != state.AppActive {
			t.Fatalf("enforce app parked after %s with unchanged image and database", clock.Sub(start))
		}
		clock = clock.Add(time.Minute)
	}
	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := decodeScanEvidence(got.ScanResult)
	if result == nil || result.ScannedAt != scannedAt || result.VerifiedAt == "" {
		t.Fatalf("evidence = %+v, want the original scanned_at and a verified_at", result)
	}
}

// A renewal must not replace evidence another imaged wrote after this one
// read the row: a newer scan under a newer database may carry findings.
func TestRenewScanEvidenceLeavesANewerScanAlone(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "renew-race@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "renew-race", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:renew", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	old := []byte(`{"scanned_at":"2026-10-29T00:00:00Z","severity_counts":{"critical":0,"high":0,"medium":0,"low":0,"unknown":0},"vulnerabilities":[]}`)
	if err := store.UpsertDeploymentScanResult(ctx, dep.ID, old, "complete"); err != nil {
		t.Fatal(err)
	}
	read, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	newer := []byte(`{"scanned_at":"2026-10-29T05:00:00Z","severity_counts":{"critical":1,"high":0,"medium":0,"low":0,"unknown":0},"vulnerabilities":[]}`)
	if err := store.UpsertDeploymentScanResult(ctx, dep.ID, newer, "complete"); err != nil {
		t.Fatal(err)
	}
	loop := &Loop{store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	loop.renewScanEvidence(ctx, read)
	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result := decodeScanEvidence(got.ScanResult); result == nil || result.SeverityCounts.Critical != 1 || result.VerifiedAt != "" {
		t.Fatalf("evidence after a stale renewal = %s, want the newer scan untouched", got.ScanResult)
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
	dep.ScanResult = encode(func() ScanResult {
		result := base
		result.ScannedAt = now.Add(-3 * time.Hour).Format(time.RFC3339Nano)
		result.VerifiedAt = now.Add(-time.Hour).Format(time.RFC3339Nano)
		return result
	}())
	if got := securityScanLeaseFailure(dep, now, 2*time.Hour); got != "" {
		t.Fatalf("renewed evidence lease failure = %q, want none", got)
	}
	dep.ScanResult = encode(func() ScanResult {
		result := base
		result.VerifiedAt = now.Add(-2 * time.Hour).Format(time.RFC3339Nano)
		return result
	}())
	if got := securityScanLeaseFailure(dep, now, 2*time.Hour); got != "security_scan_evidence_invalid" {
		t.Fatalf("verified before scanned reason = %q, want invalid", got)
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

func TestReconcileSecuritySignaturesQuarantinesRevokedSigner(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "security-signature@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID:      account.ID,
		Slug:           "signature-app",
		Status:         state.AppActive,
		SecurityPolicy: api.AppSecurityPolicyEnforce,
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "registry.example.com/app@sha256:revoked",
		Status:      state.DeployLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	notif := &fakeNotifier{}
	handler := &Handler{
		store: store,
		notif: notif,
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	// The app's last signer was removed: app_trusted_signers has no rows,
	// which fails the revalidation closed.
	loop := &Loop{
		store:   store,
		handler: handler,
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:     func() time.Time { return time.Date(2026, 9, 19, 15, 0, 0, 0, time.UTC) },
	}
	loop.reconcileSecuritySignatures(ctx, loop.now())

	gotApp, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotApp.Status != state.AppEvictedCold {
		t.Fatalf("app status = %q, want evicted_cold", gotApp.Status)
	}
	gotDep, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotDep.ParkedReason != string(state.ParkReasonSecurityScanRegressed) {
		t.Fatalf("parked reason = %q, want security_scan_regressed", gotDep.ParkedReason)
	}
	appChanged := findNotify(notif, db.NotifyAppChanged)
	if appChanged == nil {
		t.Fatal("signature quarantine did not notify app_changed")
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(appChanged.payload), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["evidence_reason"] != "security_signature_revoked" || payload["deployment_id"] != dep.ID {
		t.Fatalf("notification payload = %v, want revoked signer evidence", payload)
	}
	audit := findNotify(notif, db.NotifyAuditEvent)
	if audit == nil {
		t.Fatal("signature quarantine did not notify audit_event")
	}
	var auditPayload signatureAuditPayload
	if err := json.Unmarshal([]byte(audit.payload), &auditPayload); err != nil {
		t.Fatal(err)
	}
	if auditPayload.Kind != "app.signature_revoked" {
		t.Fatalf("audit kind = %q, want app.signature_revoked", auditPayload.Kind)
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
