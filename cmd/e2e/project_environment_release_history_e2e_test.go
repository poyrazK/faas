// project_environment_release_history_e2e_test.go — general-path acceptance
// for project environment release inventory and promotion history.
//
// These APIs sit after deployment and promotion state has been written. They
// need an HTTP/Postgres check because handler-only tests do not prove that the
// route is account-scoped, that live/undeployed workloads are reported
// consistently, or that an opaque cursor remains stable across filtered pages.
//
// Build tag: (none). CI-safe. No schedd, vmmd, or KVM is required.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func projectEnvironmentReleaseInventory(t *testing.T, f *projectEnvironmentPromotionFixture, environment string) api.ProjectEnvironmentReleaseListResponse {
	t.Helper()
	path := "/v1/projects/" + f.project.Slug + "/environments/" + environment + "/releases"
	raw, status := doReq(t, f.h, f.key, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status=%d body=%s", path, status, raw)
	}
	var response api.ProjectEnvironmentReleaseListResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode release inventory: %v body=%s", err, raw)
	}
	return response
}

func releaseInventoryWorkload(t *testing.T, response api.ProjectEnvironmentReleaseListResponse, slug string) api.ProjectEnvironmentReleaseWorkloadResponse {
	t.Helper()
	for _, workload := range response.Workloads {
		if workload.WorkloadSlug == slug {
			return workload
		}
	}
	t.Fatalf("release workload %q not found: %+v", slug, response.Workloads)
	return api.ProjectEnvironmentReleaseWorkloadResponse{}
}

func projectEnvironmentPromotionHistory(t *testing.T, f *projectEnvironmentPromotionFixture, query string) api.ProjectEnvironmentPromotionListResponse {
	t.Helper()
	path := "/v1/projects/" + f.project.Slug + "/environments/production/promotions" + query
	raw, status := doReq(t, f.h, f.key, http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s: status=%d body=%s", path, status, raw)
	}
	var response api.ProjectEnvironmentPromotionListResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode promotion history: %v body=%s", err, raw)
	}
	return response
}

func performReleaseHistoryPromotion(t *testing.T, f *projectEnvironmentPromotionFixture) api.ProjectEnvironmentPromotionResponse {
	t.Helper()
	previewPath := "/v1/projects/" + f.project.Slug + "/environments/production/promotion-preview?from=staging"
	raw, status := doReq(t, f.h, f.key, http.MethodGet, previewPath, nil)
	if status != http.StatusOK {
		t.Fatalf("promotion preview: status=%d body=%s", status, raw)
	}
	var preview api.ProjectEnvironmentPromotionPreviewResponse
	if err := json.Unmarshal(raw, &preview); err != nil {
		t.Fatalf("decode promotion preview: %v body=%s", err, raw)
	}

	approvalPath := "/v1/projects/" + f.project.Slug + "/environments/production/approvals"
	raw, status = doReq(t, f.h, f.key, http.MethodPost, approvalPath,
		api.CreateProjectEnvironmentApprovalRequest{PromotionToken: preview.PromotionToken},
		map[string]string{"Idempotency-Key": "release-history-approval"})
	if status != http.StatusCreated {
		t.Fatalf("create promotion approval: status=%d body=%s", status, raw)
	}
	var approval api.ProjectEnvironmentApprovalResponse
	if err := json.Unmarshal(raw, &approval); err != nil {
		t.Fatalf("decode promotion approval: %v body=%s", err, raw)
	}
	if approval.ApprovalToken == "" {
		t.Fatalf("approval omitted token: %+v", approval)
	}

	promotePath := "/v1/projects/" + f.project.Slug + "/environments/production/promote"
	raw, status = doReq(t, f.h, f.key, http.MethodPost, promotePath, api.PromoteProjectEnvironmentRequest{
		FromEnvironment: "staging",
		PromotionToken:  preview.PromotionToken,
		ApprovalToken:   approval.ApprovalToken,
	}, map[string]string{"Idempotency-Key": "release-history-promotion"})
	if status != http.StatusOK {
		t.Fatalf("promote: status=%d body=%s", status, raw)
	}
	var promoted api.ProjectEnvironmentPromotionResponse
	if err := json.Unmarshal(raw, &promoted); err != nil {
		t.Fatalf("decode promotion response: %v body=%s", err, raw)
	}
	if promoted.PromotionID == "" || promoted.PromotionHash != preview.PromotionHash {
		t.Fatalf("promotion identity=%+v preview=%+v", promoted, preview)
	}
	return promoted
}

// TestE2E_ProjectEnvironmentReleaseHistory_ReadAfterPromotionAndRollback
// verifies that the read APIs expose the durable state transitions customers
// rely on: every workload is present, live deployment metadata changes after
// promotion, and rollback restores the original inventory while retaining a
// useful history record.
func TestE2E_ProjectEnvironmentReleaseHistory_ReadAfterPromotionAndRollback(t *testing.T) {
	f := newProjectEnvironmentPromotionFixture(t, "release-read")
	if f == nil {
		return
	}

	production := projectEnvironmentReleaseInventory(t, f, "production")
	if production.ProjectSlug != f.project.Slug || production.Environment != "production" || len(production.Workloads) != 2 {
		t.Fatalf("initial production inventory=%+v", production)
	}
	frontend := releaseInventoryWorkload(t, production, "frontend")
	worker := releaseInventoryWorkload(t, production, "worker")
	if frontend.Status != "live" || frontend.DeploymentID != f.apiTarget.ID || frontend.ImageDigest != f.apiTarget.ImageDigest || frontend.SourceSHA256 != f.apiTarget.SourceSHA256 || frontend.TrafficPercent != 100 || frontend.CreatedAt == "" {
		t.Fatalf("initial frontend release=%+v target=%+v", frontend, f.apiTarget)
	}
	if worker.Status != "not_deployed" || worker.DeploymentID != "" {
		t.Fatalf("initial worker release=%+v", worker)
	}

	staging := projectEnvironmentReleaseInventory(t, f, "staging")
	if len(staging.Workloads) != 2 {
		t.Fatalf("staging inventory=%+v", staging)
	}
	stagingFrontend := releaseInventoryWorkload(t, staging, "frontend")
	stagingWorker := releaseInventoryWorkload(t, staging, "worker")
	if stagingFrontend.Status != "live" || stagingFrontend.DeploymentID != f.apiSource.ID || stagingFrontend.ImageDigest != f.apiSource.ImageDigest || stagingFrontend.SourceSHA256 != f.apiSource.SourceSHA256 || stagingFrontend.TrafficPercent != 100 {
		t.Fatalf("staging frontend release=%+v source=%+v", stagingFrontend, f.apiSource)
	}
	if stagingWorker.Status != "live" || stagingWorker.DeploymentID != f.workerSource.ID || stagingWorker.ImageDigest != f.workerSource.ImageDigest || stagingWorker.SourceSHA256 != f.workerSource.SourceSHA256 || stagingWorker.TrafficPercent != 100 {
		t.Fatalf("staging worker release=%+v source=%+v", stagingWorker, f.workerSource)
	}

	promoted := performReleaseHistoryPromotion(t, f)
	promotionTargetBySlug := map[string]string{}
	for _, workload := range promoted.Workloads {
		promotionTargetBySlug[workload.WorkloadSlug] = workload.TargetDeploymentID
	}
	if promotionTargetBySlug["frontend"] == "" || promotionTargetBySlug["worker"] == "" {
		t.Fatalf("promotion targets=%+v", promoted.Workloads)
	}

	production = projectEnvironmentReleaseInventory(t, f, "production")
	frontend = releaseInventoryWorkload(t, production, "frontend")
	worker = releaseInventoryWorkload(t, production, "worker")
	if frontend.Status != "live" || frontend.DeploymentID != promotionTargetBySlug["frontend"] || frontend.DeploymentID == f.apiTarget.ID || frontend.ImageDigest != f.apiSource.ImageDigest || frontend.SourceSHA256 != f.apiSource.SourceSHA256 || frontend.TrafficPercent != 100 {
		t.Fatalf("promoted frontend release=%+v", frontend)
	}
	if worker.Status != "live" || worker.DeploymentID != promotionTargetBySlug["worker"] || worker.ImageDigest != f.workerSource.ImageDigest || worker.SourceSHA256 != f.workerSource.SourceSHA256 || worker.TrafficPercent != 100 {
		t.Fatalf("promoted worker release=%+v", worker)
	}

	history := projectEnvironmentPromotionHistory(t, f, "?from=staging&status=succeeded&limit=10")
	if len(history.Items) != 1 || history.Items[0].PromotionID != promoted.PromotionID || history.Items[0].PromotionHash != promoted.PromotionHash || history.Items[0].ProjectSlug != f.project.Slug || history.Items[0].FromEnvironment != "staging" || history.Items[0].ToEnvironment != "production" || history.Items[0].Status != "succeeded" || history.Items[0].VerificationStatus != "verified" || history.Items[0].RollbackStatus != "" || history.NextBefore != "" {
		t.Fatalf("promotion history after promote=%+v", history)
	}

	rollbackPath := "/v1/projects/" + f.project.Slug + "/environments/production/promotions/" + promoted.PromotionID + "/rollback"
	raw, status := doReq(t, f.h, f.key, http.MethodPost, rollbackPath, nil,
		map[string]string{"Idempotency-Key": "release-history-rollback"})
	if status != http.StatusOK {
		t.Fatalf("rollback: status=%d body=%s", status, raw)
	}
	var rollback api.ProjectEnvironmentPromotionStatusResponse
	if err := json.Unmarshal(raw, &rollback); err != nil {
		t.Fatalf("decode rollback: %v body=%s", err, raw)
	}
	if rollback.RollbackStatus != "rolled_back" {
		t.Fatalf("rollback response=%+v", rollback)
	}

	production = projectEnvironmentReleaseInventory(t, f, "production")
	frontend = releaseInventoryWorkload(t, production, "frontend")
	worker = releaseInventoryWorkload(t, production, "worker")
	if frontend.Status != "live" || frontend.DeploymentID != f.apiTarget.ID || frontend.ImageDigest != f.apiTarget.ImageDigest || frontend.SourceSHA256 != f.apiTarget.SourceSHA256 || frontend.TrafficPercent != 100 {
		t.Fatalf("restored frontend release=%+v", frontend)
	}
	if worker.Status != "not_deployed" || worker.DeploymentID != "" {
		t.Fatalf("restored worker release=%+v", worker)
	}

	history = projectEnvironmentPromotionHistory(t, f, "?from=staging&status=succeeded&limit=10")
	if len(history.Items) != 1 || history.Items[0].PromotionID != promoted.PromotionID || history.Items[0].RollbackStatus != "rolled_back" || history.Items[0].VerificationStatus != "verified" || history.Items[0].RollbackError != "" {
		t.Fatalf("promotion history after rollback=%+v", history)
	}
}

func seedReleaseHistoryPromotion(t *testing.T, f *projectEnvironmentPromotionFixture, from, status, idempotencyKey, promotionHash string, createdAt time.Time) string {
	t.Helper()
	promotion, _, err := f.store.CreateProjectEnvironmentPromotion(context.Background(), state.ProjectEnvironmentPromotion{
		AccountID: f.project.AccountID, ProjectID: f.project.ID, ProjectSlug: f.project.Slug,
		FromEnvironment: from, ToEnvironment: "production", PromotionHash: promotionHash,
		IdempotencyKey: idempotencyKey, Status: status,
		VerificationStatus: map[string]string{"succeeded": "verified", "failed": "failed"}[status],
	}, nil)
	if err != nil {
		t.Fatalf("seed promotion history: %v", err)
	}
	if _, err := f.h.Pool.Exec(context.Background(), `
		update project_environment_promotions
		   set created_at = $1, updated_at = $1
		 where id = $2`, createdAt.UTC(), promotion.ID); err != nil {
		t.Fatalf("set promotion history timestamp: %v", err)
	}
	return promotion.ID
}

// TestE2E_ProjectEnvironmentPromotionHistory_PaginationFiltersAndIsolation
// covers the query semantics that are easy to get wrong at the HTTP boundary:
// cursor continuation, source/status filters, malformed cursors, and tenant
// isolation of a project slug.
func TestE2E_ProjectEnvironmentPromotionHistory_PaginationFiltersAndIsolation(t *testing.T) {
	f := newProjectEnvironmentPromotionFixture(t, "history-query")
	if f == nil {
		return
	}
	promoted := performReleaseHistoryPromotion(t, f)
	base := time.Now().UTC().Add(-10 * time.Minute)
	olderSucceededID := seedReleaseHistoryPromotion(t, f, "staging", "succeeded", "history-seeded-succeeded", strings.Repeat("b", 64), base.Add(1*time.Minute))
	failedID := seedReleaseHistoryPromotion(t, f, "staging", "failed", "history-seeded-failed", strings.Repeat("c", 64), base)

	first := projectEnvironmentPromotionHistory(t, f, "?from=staging&status=succeeded&limit=1")
	if len(first.Items) != 1 || first.Items[0].PromotionID != promoted.PromotionID || first.NextBefore == "" {
		t.Fatalf("first filtered history page=%+v", first)
	}
	second := projectEnvironmentPromotionHistory(t, f, "?from=staging&status=succeeded&limit=1&before="+first.NextBefore)
	if len(second.Items) != 1 || second.Items[0].PromotionID != olderSucceededID || second.NextBefore != "" {
		t.Fatalf("second filtered history page=%+v", second)
	}

	failed := projectEnvironmentPromotionHistory(t, f, "?from=staging&status=failed")
	if len(failed.Items) != 1 || failed.Items[0].PromotionID != failedID || failed.Items[0].Status != "failed" || failed.Items[0].FromEnvironment != "staging" {
		t.Fatalf("failed history filter=%+v", failed)
	}

	assertPromotionProblem(t, f.h, f.key, http.MethodGet,
		"/v1/projects/"+f.project.Slug+"/environments/production/promotions?before=not-a-cursor",
		nil, "history-invalid-cursor", http.StatusBadRequest, api.CodeValidation)

	otherKey := f.h.SeedAccount(context.Background(), api.PlanPro, "history-other-account")
	assertPromotionProblem(t, f.h, otherKey, http.MethodGet,
		"/v1/projects/"+f.project.Slug+"/environments/production/promotions",
		nil, "history-cross-account", http.StatusNotFound, api.CodeNotFound)
}
