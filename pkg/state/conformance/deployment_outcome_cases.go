// spec: §12
package conformance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type deploymentOutcomeStore interface {
	CountDeploymentOutcomesSince(context.Context, time.Time, ...string) (state.DeploymentOutcomeCounts, error)
	LatestPlatformDeploymentOutcome(context.Context, ...string) (state.LatestDeploymentOutcome, error)
}

// testDeploymentOutcomesExcludeCustomerCausedFailures pins that the public
// status page's deployment-success aggregate is platform health: a tenant
// whose app crashes or never listens must not degrade it (production-us
// hunt #9, H9-106: two deliberately broken test apps put "deployments
// degraded, 87%" on the public status page).
func testDeploymentOutcomesExcludeCustomerCausedFailures(t *testing.T, fx *Fixture) {
	outcomes, ok := fx.Store.(deploymentOutcomeStore)
	if !ok {
		t.Skip("store has no deployment outcome aggregate")
	}
	since := time.Now().Add(-time.Hour)
	fail := func(code string) {
		t.Helper()
		dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
			AppID: fx.App.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending,
		})
		if err != nil {
			t.Fatalf("CreateDeployment(%s): %v", code, err)
		}
		if _, err := fx.Store.SetDeploymentFailed(fx.Ctx, dep.ID, code, "snapshot prime failed"); err != nil {
			t.Fatalf("SetDeploymentFailed(%s): %v", code, err)
		}
	}

	fail("internal")
	live, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
		AppID: fx.App.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(live): %v", err)
	}
	if err := fx.Store.MarkDeploymentLive(fx.Ctx, live.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	fail(api.CodeAppNotListening)
	fail(api.CodeAppStartupCrash)

	counts, err := outcomes.CountDeploymentOutcomesSince(fx.Ctx, since)
	if err != nil {
		t.Fatalf("CountDeploymentOutcomesSince: %v", err)
	}
	// The fixture's live deployment and the later one succeeded; only the
	// internal failure is platform evidence.
	if counts.Succeeded != 2 || counts.Failed != 1 {
		t.Fatalf("outcomes = %+v, want 2 succeeded and 1 platform failure", counts)
	}
	latest, err := outcomes.LatestPlatformDeploymentOutcome(fx.Ctx)
	if err != nil {
		t.Fatalf("LatestPlatformDeploymentOutcome: %v", err)
	}
	if !latest.Succeeded {
		t.Fatalf("latest platform outcome = %+v, want the success that preceded the customer-caused failures", latest)
	}
}

// testDeploymentOutcomesAttributeAppAnswersAndRetries pins the 2026-11-22
// status attribution (prod: two internal accounts' retries and an app's own
// 422 put "deployments degraded" on the public status page): a smoke check
// the candidate itself answered is the app's verdict, repeated failures of
// one app with one code count once, and excluded accounts leave the ratio.
func testDeploymentOutcomesAttributeAppAnswersAndRetries(t *testing.T, fx *Fixture) {
	outcomes, ok := fx.Store.(deploymentOutcomeStore)
	if !ok {
		t.Skip("store has no deployment outcome aggregate")
	}
	receipts, ok := fx.Store.(interface {
		UpsertDeploymentHostingReceipt(context.Context, string, []byte) (state.Deployment, error)
	})
	if !ok {
		t.Skip("store has no hosting receipts")
	}
	since := time.Now().Add(-time.Hour)
	fail := func(code string, receipt string) {
		t.Helper()
		dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
			AppID: fx.App.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending,
		})
		if err != nil {
			t.Fatalf("CreateDeployment(%s): %v", code, err)
		}
		if _, err := fx.Store.SetDeploymentFailed(fx.Ctx, dep.ID, code, "failed"); err != nil {
			t.Fatalf("SetDeploymentFailed(%s): %v", code, err)
		}
		if receipt != "" {
			if _, err := receipts.UpsertDeploymentHostingReceipt(fx.Ctx, dep.ID, []byte(receipt)); err != nil {
				t.Fatalf("UpsertDeploymentHostingReceipt: %v", err)
			}
		}
	}
	// The candidate answered 422 with proof: the app's verdict.
	fail(api.CodeDeploymentSmokeFailed, `{"smoke":{"status":"failed","status_code":422,"error_code":"`+api.CustomerCausedSmokeErrorCode+`"}}`)
	counts, err := outcomes.CountDeploymentOutcomesSince(fx.Ctx, since)
	if err != nil || counts.Failed != 0 {
		t.Fatalf("outcomes after a proven 422 = %+v, %v; want no platform failure", counts, err)
	}
	// A proven 502 can come from the platform's bridge hop: it counts.
	fail(api.CodeDeploymentSmokeFailed, `{"smoke":{"status":"failed","status_code":502,"error_code":"`+api.CustomerCausedSmokeErrorCode+`"}}`)
	counts, err = outcomes.CountDeploymentOutcomesSince(fx.Ctx, since)
	if err != nil || counts.Failed != 1 {
		t.Fatalf("outcomes after a proven 502 = %+v, %v; want one platform failure", counts, err)
	}
	// Unreachable verification of the same app in the same hour is the same
	// problem as the 502, three times over.
	for range 3 {
		fail(api.CodeDeploymentSmokeFailed, `{"smoke":{"status":"failed","error_code":"smoke_verification_unavailable"}}`)
	}
	fail("internal", "")

	counts, err = outcomes.CountDeploymentOutcomesSince(fx.Ctx, since)
	if err != nil {
		t.Fatalf("CountDeploymentOutcomesSince: %v", err)
	}
	// Two distinct platform problems on one app: smoke failed (502 and
	// unavailable ×3, one code) and internal. The fixture's live deployment
	// is the one success. (An hour boundary between the failures would split
	// the smoke key; the cases run within milliseconds.)
	if counts.Succeeded != 1 || counts.Failed != 2 || counts.FailedAccounts != 1 {
		t.Fatalf("outcomes = %+v, want 1 succeeded, 2 distinct platform failures, 1 account", counts)
	}
	excluded, err := outcomes.CountDeploymentOutcomesSince(fx.Ctx, since, fx.Account.ID)
	if err != nil {
		t.Fatalf("CountDeploymentOutcomesSince(excluded): %v", err)
	}
	if excluded != (state.DeploymentOutcomeCounts{}) {
		t.Fatalf("excluded account outcomes = %+v, want none", excluded)
	}
	if _, err := outcomes.LatestPlatformDeploymentOutcome(fx.Ctx, fx.Account.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("LatestPlatformDeploymentOutcome(excluded) err = %v, want ErrNotFound", err)
	}
}
