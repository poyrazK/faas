package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectEnvironmentRollbackReportsBreakingContract(t *testing.T) {
	t.Setenv("FAAS_API_CONTRACT_DIFF_ENABLED", "1")
	srv, store, acct, project, app := apidserver.NewProjectLifecycleFixture(t)
	previous := createRollbackContractDeployment(t, store, app, "production", false)
	promotionID := "project-rollback-contract"
	candidate, err := store.CreateDeployment(t.Context(), state.Deployment{
		AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat("2", 64), Status: state.DeployPending,
		Reason: apidserver.ProjectEnvironmentPromotionDeploymentReason(promotionID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SetDeploymentRootfs(t.Context(), candidate.ID, "/rootfs/promoted", "apps/promoted.ext4", 42); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDeploymentLive(t.Context(), candidate.ID); err != nil {
		t.Fatal(err)
	}
	writeRollbackContractSnapshot(t, store, previous, rollbackContractOld)
	writeRollbackContractSnapshot(t, store, candidate, rollbackContractNew)
	if _, _, err = store.CreateProjectEnvironmentPromotion(t.Context(), state.ProjectEnvironmentPromotion{
		ID: promotionID, AccountID: acct.ID, ProjectID: project.ID, ProjectSlug: project.Slug,
		FromEnvironment: "staging", ToEnvironment: "production", PromotionHash: strings.Repeat("a", 64),
		IdempotencyKey: "project-contract-promotion", Status: "succeeded",
	}, []state.ProjectEnvironmentPromotionWorkload{{
		WorkloadSlug: app.Slug, WorkloadName: app.WorkloadName, SourceDeploymentID: "source",
		TargetDeploymentID: candidate.ID, PreviousTargetDeploymentID: previous.ID,
		PreviousTargetTrafficPercent: 100, Status: "promoted",
	}}); err != nil {
		t.Fatal(err)
	}
	req, rec := apidserver.ProjectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promotions/"+promotionID+"/rollback", "shop", nil)
	req.SetPathValue("environment", "production")
	req.SetPathValue("promotion", promotionID)
	req.Header.Set("Idempotency-Key", "project-contract-rollback")
	srv.RollbackProjectEnvironmentPromotion(rec, req, acct)
	var problem api.Problem
	if err = json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != http.StatusUnprocessableEntity || problem.Code != api.CodeAPIContractBreakingChange {
		t.Fatalf("contract refusal: %d %s (%v)", rec.Code, rec.Body, err)
	}
	if serving, err := store.LiveDeploymentForScope(t.Context(), app.ID, "production"); err != nil || serving.ID != candidate.ID {
		t.Fatalf("contract refusal changed serving deployment: %+v %v", serving, err)
	}
	result, workloads, err := store.ProjectEnvironmentPromotionByID(t.Context(), acct.ID, project.Slug, "production", promotionID)
	if err != nil || result.RollbackStatus != "rollback_failed" || len(workloads) != 1 || workloads[0].RollbackStatus != "failed" {
		t.Fatalf("contract refusal missing from durable status: %+v %+v %v", result, workloads, err)
	}
}
