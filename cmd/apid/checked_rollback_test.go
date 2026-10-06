package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func checkedRollbackAPIFixture(t *testing.T) (testEnv, state.App, state.Deployment, state.Deployment) {
	t.Helper()
	e, app, target, current := promotionFixture(t)
	if _, err := e.store.UpdateDeploymentTraffic(t.Context(), current.ID, 100); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(t.Context(), current.ID); err != nil {
		t.Fatal(err)
	}
	return e, app, target, current
}
func TestCheckedRollbackAPIWorkerAndRestart(t *testing.T) {
	e, app, target, current := checkedRollbackAPIFixture(t)
	ctx := t.Context()
	zero := int64(0)
	if _, err := e.store.SetBindingReleasePolicy(ctx, e.acct.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	body := api.RollbackRequest{TargetDeploymentID: &target.ID, ExpectedCurrentDeploymentID: &current.ID, Reason: "restore older release"}
	path := "/v1/apps/" + app.Slug + "/rollback"
	response := e.do(t, http.MethodPost, path, body, map[string]string{"Idempotency-Key": "exact-historical-rollback"})
	var receipt api.DeploymentResponse
	if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &receipt) != nil || receipt.RollbackOperation == nil || receipt.RollbackOperation.Status != "preparing" {
		t.Fatalf("accepted receipt: %d %s", response.Code, response.Body)
	}
	operation := *receipt.RollbackOperation
	replay := e.do(t, http.MethodPost, path, body, map[string]string{"Idempotency-Key": "exact-historical-rollback"})
	var same api.DeploymentResponse
	if replay.Code != 202 || json.Unmarshal(replay.Body.Bytes(), &same) != nil || same.RollbackOperation == nil || same.RollbackOperation.ID != operation.ID {
		t.Fatalf("idempotency replay changed intent: %d %s", replay.Code, replay.Body)
	}
	assertPromotionWeights(t, e, target, current, 100)
	if err := e.store.MarkDeploymentLive(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.s.checkedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	status := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/rollbacks/"+operation.ID, nil, nil)
	var blocked api.RollbackOperation
	if status.Code != 200 || json.Unmarshal(status.Body.Bytes(), &blocked) != nil || blocked.Status != "blocked" || len(blocked.Blockers) == 0 {
		t.Fatalf("missing binding blockers: %d %s", status.Code, status.Body)
	}
	assertPromotionWeights(t, e, target, current, 100)
	completePromotionProbe(t, e, app, target, passedPostgresVerification)
	old := e.s
	e.s = newServer(e.store, old.log, "gregale.dev", noopNotifier{}).WithManagedPostgres(old.managedPostgres, nil, old.managedPostgresBindings, nil, nil, nil).WithRollbackArtifactVerifier(stubRollbackArtifactVerifier{})
	if err := e.s.checkedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	finished, err := e.store.GetCheckedRollback(ctx, e.acct.ID, app.ID, operation.ID)
	if err != nil || finished.Status != "complete" || finished.AuditID == "" || finished.CompletedAt == nil {
		t.Fatalf("restart did not resume exact rollback: %+v %v", finished, err)
	}
	got, _ := e.store.DeploymentByID(ctx, target.ID)
	prior, _ := e.store.DeploymentByID(ctx, current.ID)
	if got.TrafficPercent != 100 || prior.TrafficPercent != 0 {
		t.Fatalf("wrong deployment restored: %+v %+v", got, prior)
	}
}
func TestCheckedRollbackAPIRejectsLegacyAndChangedSelection(t *testing.T) {
	for _, scenario := range []string{"legacy", "different current", "same target", "missing artifact"} {
		t.Run(scenario, func(t *testing.T) {
			e, app, target, current := checkedRollbackAPIFixture(t)
			zero := int64(0)
			if _, err := e.store.SetBindingReleasePolicy(t.Context(), e.acct.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
				t.Fatal(err)
			}
			req := api.RollbackRequest{TargetDeploymentID: &target.ID, ExpectedCurrentDeploymentID: &current.ID}
			switch scenario {
			case "legacy":
				req.ExpectedCurrentDeploymentID = nil
			case "different current":
				wrong := uuid.NewString()
				req.ExpectedCurrentDeploymentID = &wrong
			case "same target":
				req.ExpectedCurrentDeploymentID = &target.ID
			case "missing artifact":
				e.s.rollbackArtifactVerifier = stubRollbackArtifactVerifier{err: state.ErrNotFound}
			}
			response := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/rollback", req, nil)
			if response.Code < 400 {
				t.Fatalf("unsafe request accepted: %d %s", response.Code, response.Body)
			}
			got, _ := e.store.DeploymentByID(t.Context(), current.ID)
			old, _ := e.store.DeploymentByID(t.Context(), target.ID)
			if got.TrafficPercent != 100 || old.Status != state.DeployLive || old.TrafficPercent != 0 {
				t.Fatalf("rejected intent changed routing/preparation: %+v %+v", got, old)
			}
		})
	}
}
func TestCheckedRollbackAPIConcurrentReleaseFailsWhileBindingsBlocked(t *testing.T) {
	e, app, target, current := checkedRollbackAPIFixture(t)
	ctx := t.Context()
	operation, err := e.store.CreateCheckedRollback(ctx, e.acct.ID, app.ID, target.ID, current.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.store.MarkDeploymentLive(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	newer, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployPending, ImageDigest: "sha256:newer", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.store.MarkDeploymentLive(ctx, newer.ID); err != nil {
		t.Fatal(err)
	}
	if err = e.s.checkedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	failed, err := e.store.GetCheckedRollback(ctx, e.acct.ID, app.ID, operation.ID)
	if err != nil || failed.Status != "failed" {
		t.Fatalf("concurrent release not terminal: %+v %v", failed, err)
	}
}

func TestCheckedRollbackAPIServiceWaitsForMatchingHandoff(t *testing.T) {
	e, app, target, current := checkedRollbackAPIFixture(t)
	ctx := t.Context()
	manifest := app.Manifest
	manifest.ExecutionMode = api.ExecutionModeService
	manifest.ServiceReplicas = &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1}
	var err error
	app, err = e.store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := e.store.CreateCheckedRollback(ctx, e.acct.ID, app.ID, target.ID, current.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.store.MarkDeploymentLive(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = e.store.CreateInstanceWithMode(ctx, app.ID, target.ID, "running", 512, state.DefaultLocalNodeName, uuid.NewString(), "service"); err != nil {
		t.Fatal(err)
	}
	// The service binding worker leaves this dedicated operation to its owner.
	cursor := 0
	if err = e.s.serviceRolloutBindingSweep(ctx, &cursor); err != nil {
		t.Fatal(err)
	}
	got, _ := e.store.DeploymentByID(ctx, target.ID)
	if got.TrafficPercent != 0 {
		t.Fatal("service worker bypassed historical operation")
	}
	if err = e.s.checkedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	routed, err := e.store.GetCheckedRollback(ctx, e.acct.ID, app.ID, operation.ID)
	if err != nil || routed.Status != "routing" || routed.CompletedAt != nil {
		t.Fatalf("routing misreported completion: %+v %v", routed, err)
	}
	if err = e.s.checkedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	waiting, _ := e.store.GetCheckedRollback(ctx, e.acct.ID, app.ID, operation.ID)
	if waiting.Status != "routing" {
		t.Fatalf("completed before gateway/drain: %+v", waiting)
	}
	if _, err = e.store.FinalizeServiceRollout(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	if err = e.s.checkedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	finished, err := e.store.GetCheckedRollback(ctx, e.acct.ID, app.ID, operation.ID)
	if err != nil || finished.Status != "complete" || finished.CompletedAt == nil || finished.AuditID != routed.AuditID {
		t.Fatalf("missing matching completion: %+v %v", finished, err)
	}
}
