package main

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

func TestProjectEnvironmentPromotionRequiresApprovalAndPromotesArtifact(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Scope: "staging", SourceSHA256: "source-staging",
		Status: state.DeployPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, source.ID, "/rootfs/source", "apps/source.ext4", 42); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Scope: "production", SourceSHA256: "source-production",
		Status: state.DeployPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, target.ID, "/rootfs/target", "apps/target.ext4", 42); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, target.ID); err != nil {
		t.Fatal(err)
	}

	previewReq, previewRec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/promotion-preview?from=staging", "shop", nil)
	previewReq.SetPathValue("environment", "production")
	srv.previewProjectEnvironmentPromotion(previewRec, previewReq, acct)
	if previewRec.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", previewRec.Code, previewRec.Body.String())
	}
	var preview api.ProjectEnvironmentPromotionPreviewResponse
	if err := json.Unmarshal(previewRec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}

	requestBody, err := json.Marshal(api.PromoteProjectEnvironmentRequest{
		FromEnvironment: "staging", PromotionToken: preview.PromotionToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	executeReq, executeRec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promote", "shop", requestBody)
	executeReq.SetPathValue("environment", "production")
	executeReq.Header.Set("Idempotency-Key", "promotion-test-1")
	srv.promoteProjectEnvironment(executeRec, executeReq, acct)
	if executeRec.Code != http.StatusConflict {
		t.Fatalf("missing approval status=%d body=%s", executeRec.Code, executeRec.Body.String())
	}

	approvalToken, approval, problem := srv.issueProjectEnvironmentPromotionApproval(ctx, acct, project.Slug, "production", preview.PromotionToken)
	if problem != nil {
		t.Fatalf("issue promotion approval: %v", problem)
	}
	approvalStatusReq, approvalStatusRec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/approvals/"+approval.ID, "shop", nil)
	approvalStatusReq.SetPathValue("environment", "production")
	approvalStatusReq.SetPathValue("approval", approval.ID)
	srv.getProjectEnvironmentApprovalStatus(approvalStatusRec, approvalStatusReq, acct)
	if approvalStatusRec.Code != http.StatusOK {
		t.Fatalf("approval status=%d body=%s", approvalStatusRec.Code, approvalStatusRec.Body.String())
	}
	var approvalStatus api.ProjectEnvironmentApprovalStatusResponse
	if err := json.Unmarshal(approvalStatusRec.Body.Bytes(), &approvalStatus); err != nil {
		t.Fatal(err)
	}
	if approvalStatus.Status != "pending" || approvalStatus.TokenKind != "promotion" || approvalStatus.ApprovalID != approval.ID {
		t.Fatalf("approval status response=%+v", approvalStatus)
	}
	requestBody, err = json.Marshal(api.PromoteProjectEnvironmentRequest{
		FromEnvironment: "staging", PromotionToken: preview.PromotionToken, ApprovalToken: approvalToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	executeReq, executeRec = projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promote", "shop", requestBody)
	executeReq.SetPathValue("environment", "production")
	executeReq.Header.Set("Idempotency-Key", "promotion-test-1")
	srv.promoteProjectEnvironment(executeRec, executeReq, acct)
	if executeRec.Code != http.StatusOK {
		t.Fatalf("promotion status=%d body=%s", executeRec.Code, executeRec.Body.String())
	}
	var response api.ProjectEnvironmentPromotionResponse
	if err := json.Unmarshal(executeRec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Workloads) != 1 || response.Workloads[0].Status != "promoted" {
		t.Fatalf("promotion response=%+v", response)
	}
	if response.PromotionID == "" {
		t.Fatal("promotion response did not include durable promotion id")
	}
	approvalStatusReq, approvalStatusRec = projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/approvals/"+approval.ID, "shop", nil)
	approvalStatusReq.SetPathValue("environment", "production")
	approvalStatusReq.SetPathValue("approval", approval.ID)
	srv.getProjectEnvironmentApprovalStatus(approvalStatusRec, approvalStatusReq, acct)
	if approvalStatusRec.Code != http.StatusOK {
		t.Fatalf("consumed approval status=%d body=%s", approvalStatusRec.Code, approvalStatusRec.Body.String())
	}
	if err := json.Unmarshal(approvalStatusRec.Body.Bytes(), &approvalStatus); err != nil {
		t.Fatal(err)
	}
	if approvalStatus.Status != "consumed" || approvalStatus.ConsumedAt == "" {
		t.Fatalf("consumed approval status response=%+v", approvalStatus)
	}
	// A fresh idempotency key cannot reuse a consumed approval.
	reuseReq, reuseRec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promote", "shop", requestBody)
	reuseReq.SetPathValue("environment", "production")
	reuseReq.Header.Set("Idempotency-Key", "promotion-test-reuse")
	srv.promoteProjectEnvironment(reuseRec, reuseReq, acct)
	if reuseRec.Code != http.StatusConflict {
		t.Fatalf("consumed approval reuse status=%d body=%s", reuseRec.Code, reuseRec.Body.String())
	}
	statusReq, statusRec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/promotions/"+response.PromotionID, "shop", nil)
	statusReq.SetPathValue("environment", "production")
	statusReq.SetPathValue("promotion", response.PromotionID)
	srv.getProjectEnvironmentPromotionStatus(statusRec, statusReq, acct)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("promotion status=%d body=%s", statusRec.Code, statusRec.Body.String())
	}
	var status api.ProjectEnvironmentPromotionStatusResponse
	if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Status != "succeeded" || len(status.Workloads) != 1 || status.Workloads[0].Status != "promoted" {
		t.Fatalf("promotion status response=%+v", status)
	}
	if status.VerificationStatus != "verified" || len(status.Workloads) != 1 || status.Workloads[0].VerificationStatus != "verified" {
		t.Fatalf("promotion verification response=%+v", status)
	}
	// A replay with the same idempotency key returns the durable result even
	// though the original target now points at the promoted deployment.
	replayReq, replayRec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promote", "shop", requestBody)
	replayReq.SetPathValue("environment", "production")
	replayReq.Header.Set("Idempotency-Key", "promotion-test-1")
	srv.promoteProjectEnvironment(replayRec, replayReq, acct)
	if replayRec.Code != http.StatusOK || replayRec.Body.String() != executeRec.Body.String() {
		t.Fatalf("promotion replay status=%d body=%s want=%s", replayRec.Code, replayRec.Body.String(), executeRec.Body.String())
	}
	live, err := store.LiveDeploymentForScope(ctx, app.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	if live.ID == target.ID || live.SourceSHA256 != source.SourceSHA256 || live.RootfsKey != "apps/source.ext4" {
		t.Fatalf("promoted live deployment=%+v", live)
	}
	rollbackReq, rollbackRec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promotions/"+response.PromotionID+"/rollback", "shop", nil)
	rollbackReq.SetPathValue("environment", "production")
	rollbackReq.SetPathValue("promotion", response.PromotionID)
	rollbackReq.Header.Set("Idempotency-Key", "rollback-test-1")
	srv.rollbackProjectEnvironmentPromotion(rollbackRec, rollbackReq, acct)
	if rollbackRec.Code != http.StatusOK {
		t.Fatalf("rollback status=%d body=%s", rollbackRec.Code, rollbackRec.Body.String())
	}
	var rollbackStatus api.ProjectEnvironmentPromotionStatusResponse
	if err := json.Unmarshal(rollbackRec.Body.Bytes(), &rollbackStatus); err != nil {
		t.Fatal(err)
	}
	if rollbackStatus.RollbackStatus != "rolled_back" || len(rollbackStatus.Workloads) != 1 || rollbackStatus.Workloads[0].RollbackStatus != "restored" || rollbackStatus.Workloads[0].RestoredTargetDeploymentID != target.ID {
		t.Fatalf("rollback response=%+v", rollbackStatus)
	}
	live, err = store.LiveDeploymentForScope(ctx, app.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	if live.ID != target.ID {
		t.Fatalf("restored live deployment=%+v want=%s", live, target.ID)
	}
	replayRollbackReq, replayRollbackRec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promotions/"+response.PromotionID+"/rollback", "shop", nil)
	replayRollbackReq.SetPathValue("environment", "production")
	replayRollbackReq.SetPathValue("promotion", response.PromotionID)
	replayRollbackReq.Header.Set("Idempotency-Key", "rollback-test-1")
	srv.rollbackProjectEnvironmentPromotion(replayRollbackRec, replayRollbackReq, acct)
	if replayRollbackRec.Code != http.StatusOK || replayRollbackRec.Body.String() != rollbackRec.Body.String() {
		t.Fatalf("rollback replay status=%d body=%s want=%s", replayRollbackRec.Code, replayRollbackRec.Body.String(), rollbackRec.Body.String())
	}
}

type corruptProjectEnvironmentPromotionVerificationStore struct {
	state.Store
}

func (s *corruptProjectEnvironmentPromotionVerificationStore) UpdateProjectEnvironmentPromotionVerification(ctx context.Context, accountID, id, status, errorMessage string, startedAt, completedAt *time.Time) (state.ProjectEnvironmentPromotion, error) {
	promotion, err := s.Store.UpdateProjectEnvironmentPromotionVerification(ctx, accountID, id, status, errorMessage, startedAt, completedAt)
	if err != nil || status != "verifying" {
		return promotion, err
	}
	_, workloads, err := s.Store.ProjectEnvironmentPromotionByID(ctx, accountID, promotion.ProjectSlug, promotion.ToEnvironment, id)
	if err != nil || len(workloads) == 0 || workloads[0].TargetDeploymentID == "" {
		return promotion, err
	}
	return promotion, s.Store.UpdateDeploymentStatus(ctx, workloads[0].TargetDeploymentID, state.DeployFailed, "verification test corruption")
}

func TestProjectEnvironmentPromotionVerificationAutomaticallyRollsBack(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", SourceSHA256: "source-staging", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, source.ID, "/rootfs/source", "apps/source.ext4", 42); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	previewReq, previewRec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/promotion-preview?from=staging", "shop", nil)
	previewReq.SetPathValue("environment", "production")
	srv.previewProjectEnvironmentPromotion(previewRec, previewReq, acct)
	if previewRec.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", previewRec.Code, previewRec.Body.String())
	}
	var preview api.ProjectEnvironmentPromotionPreviewResponse
	if err := json.Unmarshal(previewRec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	approvalToken, _, problem := srv.issueProjectEnvironmentPromotionApproval(ctx, acct, project.Slug, "production", preview.PromotionToken)
	if problem != nil {
		t.Fatalf("issue promotion approval: %v", problem)
	}
	srv.store = &corruptProjectEnvironmentPromotionVerificationStore{Store: store}
	body, err := json.Marshal(api.PromoteProjectEnvironmentRequest{FromEnvironment: "staging", PromotionToken: preview.PromotionToken, ApprovalToken: approvalToken})
	if err != nil {
		t.Fatal(err)
	}
	req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promote", "shop", body)
	req.SetPathValue("environment", "production")
	req.Header.Set("Idempotency-Key", "promotion-verification-failure")
	srv.promoteProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "automatically rolled back") {
		t.Fatalf("promotion failure status=%d body=%s", rec.Code, rec.Body.String())
	}
	promotion, workloads, err := store.ProjectEnvironmentPromotionByIdempotencyKey(ctx, acct.ID, project.Slug, "promotion-verification-failure")
	if err != nil {
		t.Fatal(err)
	}
	if promotion.VerificationStatus != "failed" || promotion.RollbackStatus != "rolled_back" || len(workloads) != 1 || workloads[0].RollbackStatus != "cleared" {
		t.Fatalf("automatic rollback state=%+v workloads=%+v", promotion, workloads)
	}
	if _, err := store.LiveDeploymentForScope(ctx, app.ID, "production"); err == nil {
		t.Fatal("failed promotion target remained live after automatic rollback")
	}
}

func TestProjectEnvironmentPromotionRollbackClearsNewTarget(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	promotionID := "promotion-no-prior"
	candidate, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Scope: "production", SourceSHA256: "promoted-source",
		Status: state.DeployPending, Reason: projectEnvironmentPromotionDeploymentReason(promotionID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, candidate.ID, "/rootfs/promoted", "apps/promoted.ext4", 42); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.CreateProjectEnvironmentPromotion(ctx, state.ProjectEnvironmentPromotion{
		ID: promotionID, AccountID: acct.ID, ProjectID: project.ID, ProjectSlug: project.Slug,
		FromEnvironment: "staging", ToEnvironment: "production", PromotionHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		IdempotencyKey: "promotion-no-prior-key", Status: "succeeded",
	}, []state.ProjectEnvironmentPromotionWorkload{{
		WorkloadSlug: app.Slug, WorkloadName: app.WorkloadName, SourceDeploymentID: "source",
		TargetDeploymentID: candidate.ID, Status: "promoted",
	}})
	if err != nil {
		t.Fatal(err)
	}

	req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promotions/"+promotionID+"/rollback", "shop", nil)
	req.SetPathValue("environment", "production")
	req.SetPathValue("promotion", promotionID)
	req.Header.Set("Idempotency-Key", "rollback-no-prior")
	srv.rollbackProjectEnvironmentPromotion(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback status=%d body=%s", rec.Code, rec.Body.String())
	}
	var status api.ProjectEnvironmentPromotionStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.RollbackStatus != "rolled_back" || len(status.Workloads) != 1 || status.Workloads[0].RollbackStatus != "cleared" {
		t.Fatalf("rollback status=%+v", status)
	}
	if _, err := store.LiveDeploymentForScope(ctx, app.ID, "production"); err == nil {
		t.Fatal("promotion-created target remained live after rollback")
	}
}

func TestProjectEnvironmentPromotionPreviewComparesLiveReleasesAndConfig(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	fromValues, fromHash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"region":"eu"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{
		AccountID: acct.ID, ProjectID: project.ID, EnvironmentSlug: "staging",
		ConfigHash: fromHash, Values: fromValues,
	}); err != nil {
		t.Fatal(err)
	}
	toValues, toHash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"region":"us"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{
		AccountID: acct.ID, ProjectID: project.ID, EnvironmentSlug: "production",
		ConfigHash: toHash, Values: toValues,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Scope: "staging", SourceSHA256: "source-staging", Status: state.DeployLive,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Scope: "production", SourceSHA256: "source-production", Status: state.DeployLive,
	}); err != nil {
		t.Fatal(err)
	}

	req, rec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/promotion-preview?from=staging", "shop", nil)
	req.SetPathValue("environment", "production")
	srv.previewProjectEnvironmentPromotion(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", rec.Code, rec.Body.String())
	}
	var preview api.ProjectEnvironmentPromotionPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.FromEnvironment != "staging" || preview.ToEnvironment != "production" {
		t.Fatalf("environments=%+v", preview)
	}
	if !preview.ToEnvironmentProtected || !preview.ApprovalRequired || !preview.CanPromote {
		t.Fatalf("protection/eligibility=%+v", preview)
	}
	if len(preview.ConfigDiff.Changes) != 1 || preview.ConfigDiff.Changes[0].Key != "region" {
		t.Fatalf("config diff=%+v", preview.ConfigDiff)
	}
	if len(preview.Changes) != 1 || preview.Changes[0].Kind != "update" || preview.Changes[0].SourceRevision != "source-staging" {
		t.Fatalf("release changes=%+v", preview.Changes)
	}
	if len(preview.PromotionHash) != 64 || preview.PromotionToken == "" {
		t.Fatalf("promotion identity=%+v", preview)
	}
}

func TestProjectEnvironmentPromotionPreviewBlocksMissingSourceRelease(t *testing.T) {
	srv, store, acct, project, _ := newProjectLifecycleFixture(t)
	if _, err := store.CreateProjectEnvironment(context.Background(), state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	req, rec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/promotion-preview?from=staging", "shop", nil)
	req.SetPathValue("environment", "production")
	srv.previewProjectEnvironmentPromotion(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", rec.Code, rec.Body.String())
	}
	var preview api.ProjectEnvironmentPromotionPreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.CanPromote || len(preview.BlockingReasons) != 1 || preview.Changes[0].Kind != "source_missing" {
		t.Fatalf("blocked preview=%+v", preview)
	}
}
