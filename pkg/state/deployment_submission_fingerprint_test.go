package state_test

// adr: 20261021101500

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploymentSubmissionFingerprintMemory(t *testing.T) {
	testDeploymentSubmissionFingerprint(t, state.NewMemStore())
}

// A receipt keeps the fingerprint carried into creation or attachment; one
// recorded without a fingerprint reads back empty.
func testDeploymentSubmissionFingerprint(t *testing.T, store state.Store) {
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "fingerprints@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "fingerprints"})
	if err != nil {
		t.Fatal(err)
	}
	receipts := store.(state.DeploymentSubmissionStore)
	fingerprint := strings.Repeat("f", 64)
	created := state.ContextWithDeploymentSubmissionFingerprint(state.ContextWithDeploymentSubmission(ctx, strings.Repeat("1", 32)), fingerprint)
	dep, err := store.CreateDeployment(created, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64), TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	if receipt, err := receipts.DeploymentSubmission(ctx, app.ID, strings.Repeat("1", 32)); err != nil || receipt.RequestFingerprint != fingerprint || receipt.DeploymentID != dep.ID {
		t.Fatalf("created receipt=%+v %v", receipt, err)
	}
	attached := state.ContextWithDeploymentSubmissionFingerprint(ctx, strings.Repeat("e", 64))
	if receipt, err := receipts.AttachDeploymentSubmission(attached, app.ID, strings.Repeat("2", 32), dep.ID); err != nil || receipt.RequestFingerprint != strings.Repeat("e", 64) {
		t.Fatalf("attached receipt=%+v %v", receipt, err)
	}
	if receipt, err := receipts.AttachDeploymentSubmission(ctx, app.ID, strings.Repeat("3", 32), dep.ID); err != nil || receipt.RequestFingerprint != "" {
		t.Fatalf("legacy receipt=%+v %v", receipt, err)
	}
}
