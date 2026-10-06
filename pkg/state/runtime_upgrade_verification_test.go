package state_test

// adr: 607

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runtimeupgrade"
	"github.com/onebox-faas/faas/pkg/state"
)

func completeVerificationFixture(t *testing.T, s state.Store) (state.App, state.Deployment, state.Deployment, state.RuntimeUpgradeOperationRequest) {
	t.Helper()
	app, serving, candidate, r := runtimeUpgradeOperationFixture(t, s, s.(state.RuntimeReleaseStore))
	ops := s.(state.RuntimeUpgradeReservationStore)
	if _, err := ops.RegisterRuntimeUpgradeOperation(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.AdvanceRuntimeUpgradeOperation(t.Context(), claimRuntimeUpgradeOperation(t, ops)); err != nil {
		t.Fatal(err)
	}
	wake := runtimeUpgradeOperationReady(t, s, s.(state.RuntimeReleaseStore), app, candidate, r)
	request := state.RuntimeUpgradeCutoverRequest{AccountID: r.AccountID, AppID: r.AppID, DeploymentID: r.DeploymentID, ExpectedServingID: r.ServingDeploymentID, ExpectedTargetReleaseID: r.TargetReleaseID, ExpectedWakeID: wake, ExpectedQualificationReportSHA256: r.QualificationReportSHA256}
	if _, err := s.(state.RuntimeUpgradeCutoverStore).CutoverDeploymentRuntimeUpgrade(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.CancelRuntimeUpgradeOperation(t.Context(), r.AccountID, r.ID); err != nil {
		t.Fatal(err)
	}
	return app, serving, candidate, r
}

func TestRuntimeUpgradeVerificationReceiptOwnershipAndRollback(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s state.Store = state.NewMemStore()
			if backend == "postgres" {
				s, _ = pgStore(t)
			}
			app, serving, candidate, r := completeVerificationFixture(t, s)
			controls := runtimeupgrade.Controls{Store: s.(state.RuntimeUpgradeReservationStore)}
			receipts := s.(state.RuntimeUpgradeGatewayStore)
			session := uuid.NewString()
			if _, err := controls.Verify(t.Context(), uuid.NewString(), r.ID, []string{session}); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("cross-account verification", err)
			}
			if _, err := controls.Verify(t.Context(), r.AccountID, r.ID, nil); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatal("empty gateway set", err)
			}
			initial, err := controls.Verify(t.Context(), r.AccountID, r.ID, []string{session})
			if err != nil || initial.Reason != "gateway_confirmation_pending" {
				t.Fatal(initial, err)
			}
			if err := receipts.RecordRuntimeUpgradeGateway(t.Context(), uuid.NewString(), session, candidate.ID); !errors.Is(err, state.ErrConflict) {
				t.Fatal("cross-app receipt", err)
			}
			if err := receipts.RecordRuntimeUpgradeGateway(t.Context(), app.ID, session, candidate.ID); err != nil {
				t.Fatal(err)
			}
			observed, err := controls.Verify(t.Context(), r.AccountID, r.ID, []string{session})
			if err != nil || observed.Reason != "health_evidence_unavailable" || observed.ConfirmedGateways != 1 {
				t.Fatal(observed, err)
			}
			apps, err := receipts.ListRuntimeUpgradeGatewayRepairApps(t.Context(), "")
			if err != nil || !slices.Contains(apps, app.ID) {
				t.Fatal("lost notify repair omitted cutover", apps, err)
			}
			restarted, err := controls.Verify(t.Context(), r.AccountID, r.ID, []string{uuid.NewString()})
			if err != nil || restarted.Reason != "gateway_confirmation_pending" {
				t.Fatal("restart inherited process receipt", restarted, err)
			}
			if _, err := s.UpdateDeploymentTraffic(t.Context(), serving.ID, 100, candidate.ID); err != nil {
				t.Fatal(err)
			}
			reverted, err := controls.Verify(t.Context(), r.AccountID, r.ID, []string{session})
			if err != nil || reverted.Reason != "activation_inputs_changed" || reverted.Status == "verified" {
				t.Fatal("rollback retained verified status", reverted, err)
			}
			if err := receipts.RecordRuntimeUpgradeGateway(t.Context(), app.ID, session, candidate.ID); !errors.Is(err, state.ErrConflict) {
				t.Fatal("stale installed weights acknowledged", err)
			}
			historical, err := controls.Status(t.Context(), r.AccountID, r.ID)
			if err != nil || historical.Phase != state.RuntimeUpgradeComplete {
				t.Fatal("verification rewrote activation history", historical, err)
			}
		})
	}
}

func TestPgRuntimeUpgradeVerificationFreshHealthAndReceiptExpiry(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	app, _, candidate, r := completeVerificationFixture(t, s)
	// Synthetic historical cutover moves the observation window; no VM runs.
	if _, err := pool.Exec(t.Context(), `ALTER TABLE deployment_runtime_upgrade_cutovers DISABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE deployment_runtime_upgrade_cutovers SET cutover_at=clock_timestamp()-interval '6 minutes' WHERE deployment_id=$1`, candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `ALTER TABLE deployment_runtime_upgrade_cutovers ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}

	session := uuid.NewString()
	if err := s.RecordRuntimeUpgradeGateway(t.Context(), app.ID, session, candidate.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claim, err := s.ClaimAppHealth(t.Context(), uuid.NewString(), now)
	if err != nil || claim.AppID != app.ID {
		t.Fatal(claim, err)
	}
	h := api.AppHealthResponse{AppID: app.ID, Scope: "default", Status: "healthy", Phase: "serving", Summary: "Synthetic scoped health", EvaluatedAt: now.Format(time.RFC3339Nano), ValidForSeconds: 120, MetricsAsOf: now.Format(time.RFC3339Nano), ServingDeploymentIDs: []string{candidate.ID}, LatestDeploymentID: candidate.ID, Capacity: api.AppHealthCapacity{Known: true, Required: 1, Ready: 1}, Requests: &api.AppHealthRequests{Known: true, Coverage: "serving_deployments", WindowSeconds: 300, DeploymentIDs: []string{candidate.ID}, RequestCount: 100, ServerErrors: 2, ErrorRatePct: 2.9 / 100.8 * 100}}
	if err := s.FinishAppHealth(t.Context(), claim, h, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	result, err := s.VerifyRuntimeUpgrade(t.Context(), r.AccountID, r.ID, []string{session})
	if err != nil || result.Status != "verified" {
		t.Fatal("fresh exact evidence not verified", result, err)
	}
	for i := 1; i < api.RuntimeUpgradeGatewaySessionLimit; i++ {
		if err := s.RecordRuntimeUpgradeGateway(t.Context(), app.ID, uuid.NewString(), candidate.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordRuntimeUpgradeGateway(t.Context(), app.ID, uuid.NewString(), candidate.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("gateway session cap bypassed", err)
	}
	if err := s.RecordRuntimeUpgradeGateway(t.Context(), app.ID, session, candidate.ID); err != nil {
		t.Fatal("existing session retry rejected at cap", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE runtime_upgrade_gateway_receipts SET installed_at=clock_timestamp()-interval '2 minutes' WHERE app_id=$1`, app.ID); err != nil {
		t.Fatal(err)
	}
	result, err = s.VerifyRuntimeUpgrade(t.Context(), r.AccountID, r.ID, []string{session})
	if err != nil || result.Reason != "gateway_confirmation_pending" {
		t.Fatal("stale receipt accepted", result, err)
	}
	for i := 0; i < api.RuntimeUpgradeGatewaySessionLimit/api.RuntimeUpgradeGatewayRepairBatch; i++ {
		if err := s.PruneExpiredRuntimeUpgradeGatewayReceipts(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM runtime_upgrade_gateway_receipts WHERE app_id=$1`, app.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("stale receipts not pruned", count, err)
	}
	if err := s.RecordRuntimeUpgradeGateway(t.Context(), app.ID, session, candidate.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAppEnv(t.Context(), r.AccountID, app.ID, "VERIFY_DRIFT", "changed"); err != nil {
		t.Fatal(err)
	}
	result, err = s.VerifyRuntimeUpgrade(t.Context(), r.AccountID, r.ID, []string{session})
	if err != nil || result.Reason != "activation_inputs_changed" {
		t.Fatal("post-activation drift verified", result, err)
	}
}
