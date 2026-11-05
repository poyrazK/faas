//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A live rescan renews scan evidence only while the row still holds what it
// checked, so a newer scan another imaged wrote meanwhile survives (bug hunt
// 2026-10-28 B33).
func TestPgRenewDeploymentScanEvidenceIsConditional(t *testing.T) {
	store, ctx, _ := pgWithPool(t)
	acct, err := store.CreateAccount(ctx, "renew-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "renew-" + uuid.NewString()[:8], Type: state.AppTypeFunction, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	checked := []byte(`{"scanned_at":"2026-10-29T00:00:00Z","severity_counts":{"low":1}}`)
	if err := store.UpsertDeploymentScanResult(ctx, dep.ID, checked, "complete"); err != nil {
		t.Fatal(err)
	}
	renewed := []byte(`{"scanned_at":"2026-10-29T00:00:00Z","verified_at":"2026-10-29T05:00:00Z","severity_counts":{"low":1}}`)
	if ok, err := store.RenewDeploymentScanEvidence(ctx, dep.ID, checked, renewed); err != nil || !ok {
		t.Fatalf("renew current evidence = %v, %v; want renewed", ok, err)
	}
	if ok, err := store.RenewDeploymentScanEvidence(ctx, dep.ID, checked, renewed); err != nil || ok {
		t.Fatalf("renew evidence the row no longer holds = %v, %v; want refused", ok, err)
	}
	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.ScanResult) == string(checked) {
		t.Fatalf("scan_result = %s, want the renewed evidence", got.ScanResult)
	}
}
