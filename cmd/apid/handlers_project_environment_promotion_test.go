package main

import (
	"context"
	"encoding/json"
	"errors"
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

func (s *corruptProjectEnvironmentPromotionVerificationStore) ActiveProjectReleaseSet(ctx context.Context, accountID, projectID, environment string) (state.ProjectReleaseSet, error) {
	return s.Store.(state.ProjectReleaseSetReader).ActiveProjectReleaseSet(ctx, accountID, projectID, environment)
}

func (s *corruptProjectEnvironmentPromotionVerificationStore) ProjectReleaseSetByID(ctx context.Context, accountID, projectID, environment, id string) (state.ProjectReleaseSet, error) {
	return s.Store.(state.ProjectReleaseSetReader).ProjectReleaseSetByID(ctx, accountID, projectID, environment, id)
}

func (s *corruptProjectEnvironmentPromotionVerificationStore) ListProjectReleaseSetsBefore(ctx context.Context, accountID, projectID, environment string, before time.Time, beforeID string, limit int) ([]state.ProjectReleaseSet, error) {
	return s.Store.(state.ProjectReleaseSetReader).ListProjectReleaseSetsBefore(ctx, accountID, projectID, environment, before, beforeID, limit)
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

func TestProjectEnvironmentPromotionPreviewSupportsActiveReleaseGraphs(t *testing.T) {
	for _, activeEnvironment := range []string{"staging", "production"} {
		t.Run(activeEnvironment, func(t *testing.T) {
			srv, store, acct, project, app := newProjectLifecycleFixture(t)
			ctx := context.Background()
			if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
				AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
			}); err != nil {
				t.Fatal(err)
			}
			manifest := app.Manifest
			manifest.RevisionPinTTLSeconds = 3600
			if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
				t.Fatal(err)
			}

			var activeDeploymentID string
			for _, environment := range []string{"staging", "production"} {
				deployment, err := store.CreateDeployment(ctx, state.Deployment{
					AppID: app.ID, Scope: environment, ImageDigest: "sha256:" + environment, Status: state.DeployPending,
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
					t.Fatal(err)
				}
				if environment == activeEnvironment {
					activeDeploymentID = deployment.ID
				}
			}
			release, err := store.PublishProjectReleaseSet(ctx, acct.ID, project.ID, activeEnvironment, 1800,
				[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: activeDeploymentID}})
			if err != nil {
				t.Fatal(err)
			}
			var qualification state.ProjectEnvironmentQualification
			if activeEnvironment == "staging" {
				previewReq, previewRec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/promotion-preview?from=staging", "shop", nil)
				previewReq.SetPathValue("environment", "production")
				srv.previewProjectEnvironmentPromotion(previewRec, previewReq, acct)
				var blocked api.ProjectEnvironmentPromotionPreviewResponse
				if previewRec.Code != http.StatusOK || json.Unmarshal(previewRec.Body.Bytes(), &blocked) != nil ||
					blocked.CanPromote || !blocked.QualificationRequired || blocked.Qualification != nil {
					t.Fatalf("unqualified graph preview should be blocked: status=%d preview=%+v body=%s", previewRec.Code, blocked, previewRec.Body.String())
				}
				createProjectEnvironmentQualificationForTest(t, store, acct, project, "staging", release.ID, "failed", "passed")
				previewReq, previewRec = projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/promotion-preview?from=staging", "shop", nil)
				previewReq.SetPathValue("environment", "production")
				srv.previewProjectEnvironmentPromotion(previewRec, previewReq, acct)
				if previewRec.Code != http.StatusOK || json.Unmarshal(previewRec.Body.Bytes(), &blocked) != nil ||
					blocked.CanPromote || blocked.Qualification == nil || blocked.Qualification.Status != "failed" {
					t.Fatalf("failed graph qualification should block preview: status=%d preview=%+v body=%s", previewRec.Code, blocked, previewRec.Body.String())
				}
				qualification = createProjectEnvironmentQualificationForTest(t, store, acct, project, "staging", release.ID, "passed", "passed")
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
			if !preview.CanPromote || len(preview.BlockingReasons) != 0 || !preview.ReleaseGraphMode || preview.ReleaseTTLSeconds != 1800 {
				t.Fatalf("active release graph was not accepted for graph promotion: %+v", preview)
			}
			if activeEnvironment == "staging" && (preview.Qualification == nil || preview.Qualification.ID != qualification.ID ||
				preview.Qualification.Status != "passed" || !preview.QualificationRequired) {
				t.Fatalf("promotion preview did not bind the passing qualification: %+v", preview.Qualification)
			}
			graph := preview.FromReleaseSet
			if activeEnvironment == "production" {
				graph = preview.ToReleaseSet
			}
			if graph == nil || graph.ID != release.ID || !graph.Active || len(graph.Members) != 1 ||
				graph.Members[0].AppID != app.ID || graph.Members[0].DeploymentID != activeDeploymentID {
				t.Fatalf("promotion preview release graph = %+v, want release %s with deployment %s", graph, release.ID, activeDeploymentID)
			}
			wire, err := decodeProjectEnvironmentPromotionToken(preview.PromotionToken)
			if err != nil {
				t.Fatalf("decode promotion token: %v", err)
			}
			fromReleaseID, toReleaseID := "", ""
			if activeEnvironment == "staging" {
				fromReleaseID = release.ID
			} else {
				toReleaseID = release.ID
			}
			if wire.FromReleaseSetID != fromReleaseID || wire.ToReleaseSetID != toReleaseID {
				t.Fatalf("promotion token release ids = %q/%q, want %q/%q", wire.FromReleaseSetID, wire.ToReleaseSetID, fromReleaseID, toReleaseID)
			}
			if activeEnvironment == "staging" {
				values, configHash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"region":"eu"}`))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{
					AccountID: acct.ID, ProjectID: project.ID, EnvironmentSlug: "staging",
					ConfigHash: configHash, Values: values,
				}); err != nil {
					t.Fatal(err)
				}
				driftReq, driftRec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/promotion-preview?from=staging", "shop", nil)
				driftReq.SetPathValue("environment", "production")
				srv.previewProjectEnvironmentPromotion(driftRec, driftReq, acct)
				var driftPreview api.ProjectEnvironmentPromotionPreviewResponse
				if driftRec.Code != http.StatusOK || json.Unmarshal(driftRec.Body.Bytes(), &driftPreview) != nil ||
					driftPreview.CanPromote || !strings.Contains(strings.Join(driftPreview.BlockingReasons, " "), "configuration changed after qualification") {
					t.Fatalf("source config drift did not invalidate qualification: status=%d preview=%+v body=%s", driftRec.Code, driftPreview, driftRec.Body.String())
				}
			}
		})
	}
}

func TestProjectEnvironmentPromotionActivatesAndRollsBackReleaseGraph(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Scope: "staging", SourceSHA256: "graph-source-v2", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, source.ID, "/rootfs/graph-source", "apps/graph-source.ext4", 42); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	sourceGraph, err := store.PublishProjectReleaseSet(ctx, acct.ID, project.ID, "staging", 1800,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	createProjectEnvironmentQualificationForTest(t, store, acct, project, "staging", sourceGraph.ID, "passed", "passed")
	previous, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Scope: "production", SourceSHA256: "graph-production-v1", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	previousGraph, err := store.PublishProjectReleaseSet(ctx, acct.ID, project.ID, "production", 1800,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: previous.ID}})
	if err != nil {
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
	if !preview.CanPromote || !preview.ReleaseGraphMode || preview.ReleaseTTLSeconds != 1800 {
		t.Fatalf("graph promotion preview=%+v", preview)
	}
	approvalToken, _, problem := srv.issueProjectEnvironmentPromotionApproval(ctx, acct, project.Slug, "production", preview.PromotionToken)
	if problem != nil {
		t.Fatalf("issue promotion approval: %v", problem)
	}
	body, err := json.Marshal(api.PromoteProjectEnvironmentRequest{
		FromEnvironment: "staging", PromotionToken: preview.PromotionToken, ApprovalToken: approvalToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promote", "shop", body)
	req.SetPathValue("environment", "production")
	req.Header.Set("Idempotency-Key", "release-graph-promotion")
	srv.promoteProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("promotion status=%d body=%s", rec.Code, rec.Body.String())
	}
	var promoted api.ProjectEnvironmentPromotionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &promoted); err != nil {
		t.Fatal(err)
	}
	if promoted.ReleaseGraph == nil || promoted.ReleaseGraph.SourceReleaseSetID == "" ||
		promoted.ReleaseGraph.PreviousTargetReleaseSetID != previousGraph.ID ||
		promoted.ReleaseGraph.TargetReleaseSetID == "" || promoted.ReleaseGraph.TTLSeconds != 1800 {
		t.Fatalf("promoted graph metadata=%+v", promoted.ReleaseGraph)
	}
	targetID := promoted.Workloads[0].TargetDeploymentID
	if targetID == "" || targetID == previous.ID {
		t.Fatalf("promotion target deployment=%q previous=%q", targetID, previous.ID)
	}
	active, err := store.ActiveProjectReleaseSet(ctx, acct.ID, project.ID, "production")
	if err != nil || active.ID != promoted.ReleaseGraph.TargetReleaseSetID || len(active.Members) != 1 || active.Members[0].DeploymentID != targetID {
		t.Fatalf("active promoted graph=%+v err=%v", active, err)
	}
	if releaseID, deploymentID, err := store.ResolveProjectRelease(ctx, app.ID, "production", previousGraph.ID); err != nil || releaseID != previousGraph.ID || deploymentID != previous.ID {
		t.Fatalf("previous graph pin resolved %q/%q err=%v", releaseID, deploymentID, err)
	}
	weighted, err := store.LiveDeploymentForScope(ctx, app.ID, "production")
	if err != nil || weighted.ID != previous.ID {
		t.Fatalf("graph activation changed weighted route: %+v err=%v", weighted, err)
	}

	rollbackReq, rollbackRec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promotions/"+promoted.PromotionID+"/rollback", "shop", nil)
	rollbackReq.SetPathValue("environment", "production")
	rollbackReq.SetPathValue("promotion", promoted.PromotionID)
	rollbackReq.Header.Set("Idempotency-Key", "release-graph-rollback")
	srv.rollbackProjectEnvironmentPromotion(rollbackRec, rollbackReq, acct)
	if rollbackRec.Code != http.StatusOK {
		t.Fatalf("rollback status=%d body=%s", rollbackRec.Code, rollbackRec.Body.String())
	}
	var rolledBack api.ProjectEnvironmentPromotionStatusResponse
	if err := json.Unmarshal(rollbackRec.Body.Bytes(), &rolledBack); err != nil {
		t.Fatal(err)
	}
	if rolledBack.RollbackStatus != "rolled_back" || rolledBack.ReleaseGraph == nil ||
		rolledBack.ReleaseGraph.RestoredTargetReleaseSetID == "" ||
		rolledBack.ReleaseGraph.RestoredTargetReleaseSetID == previousGraph.ID {
		t.Fatalf("rollback graph metadata=%+v status=%+v", rolledBack.ReleaseGraph, rolledBack)
	}
	active, err = store.ActiveProjectReleaseSet(ctx, acct.ID, project.ID, "production")
	if err != nil || active.ID != rolledBack.ReleaseGraph.RestoredTargetReleaseSetID || len(active.Members) != 1 || active.Members[0].DeploymentID != previous.ID {
		t.Fatalf("active restored graph=%+v err=%v", active, err)
	}
	if releaseID, deploymentID, err := store.ResolveProjectRelease(ctx, app.ID, "production", promoted.ReleaseGraph.TargetReleaseSetID); err != nil || releaseID != promoted.ReleaseGraph.TargetReleaseSetID || deploymentID != targetID {
		t.Fatalf("promoted graph pin was not retained through rollback: %q/%q err=%v", releaseID, deploymentID, err)
	}
}

func TestProjectEnvironmentPromotionRollbackRestoresFallbackWithoutPriorGraph(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Scope: "staging", SourceSHA256: "fallback-source", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, source.ID, "/rootfs/fallback-source", "apps/fallback-source.ext4", 42); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	sourceGraph, err := store.PublishProjectReleaseSet(ctx, acct.ID, project.ID, "staging", 1800,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: source.ID}})
	if err != nil {
		t.Fatal(err)
	}
	createProjectEnvironmentQualificationForTest(t, store, acct, project, "staging", sourceGraph.ID, "passed", "passed")
	previous, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Scope: "production", SourceSHA256: "fallback-production", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, previous.ID); err != nil {
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
	if !preview.CanPromote || !preview.ReleaseGraphMode || preview.ToReleaseSet != nil {
		t.Fatalf("first target graph preview=%+v", preview)
	}
	approvalToken, _, problem := srv.issueProjectEnvironmentPromotionApproval(ctx, acct, project.Slug, "production", preview.PromotionToken)
	if problem != nil {
		t.Fatalf("issue promotion approval: %v", problem)
	}
	body, err := json.Marshal(api.PromoteProjectEnvironmentRequest{
		FromEnvironment: "staging", PromotionToken: preview.PromotionToken, ApprovalToken: approvalToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	promoteReq, promoteRec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promote", "shop", body)
	promoteReq.SetPathValue("environment", "production")
	promoteReq.Header.Set("Idempotency-Key", "first-release-graph-promotion")
	srv.promoteProjectEnvironment(promoteRec, promoteReq, acct)
	if promoteRec.Code != http.StatusOK {
		t.Fatalf("promotion status=%d body=%s", promoteRec.Code, promoteRec.Body.String())
	}
	var promoted api.ProjectEnvironmentPromotionResponse
	if err := json.Unmarshal(promoteRec.Body.Bytes(), &promoted); err != nil {
		t.Fatal(err)
	}
	if promoted.ReleaseGraph == nil || promoted.ReleaseGraph.PreviousTargetReleaseSetID != "" || promoted.ReleaseGraph.TargetReleaseSetID == "" {
		t.Fatalf("first graph promotion metadata=%+v", promoted.ReleaseGraph)
	}
	if weighted, err := store.LiveDeploymentForScope(ctx, app.ID, "production"); err != nil || weighted.ID != previous.ID {
		t.Fatalf("first graph activation changed weighted fallback: %+v err=%v", weighted, err)
	}

	rollbackReq, rollbackRec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promotions/"+promoted.PromotionID+"/rollback", "shop", nil)
	rollbackReq.SetPathValue("environment", "production")
	rollbackReq.SetPathValue("promotion", promoted.PromotionID)
	rollbackReq.Header.Set("Idempotency-Key", "first-release-graph-rollback")
	srv.rollbackProjectEnvironmentPromotion(rollbackRec, rollbackReq, acct)
	if rollbackRec.Code != http.StatusOK {
		t.Fatalf("rollback status=%d body=%s", rollbackRec.Code, rollbackRec.Body.String())
	}
	var rolledBack api.ProjectEnvironmentPromotionStatusResponse
	if err := json.Unmarshal(rollbackRec.Body.Bytes(), &rolledBack); err != nil {
		t.Fatal(err)
	}
	if rolledBack.RollbackStatus != "rolled_back" || rolledBack.ReleaseGraph == nil || rolledBack.ReleaseGraph.RestoredTargetReleaseSetID != "" {
		t.Fatalf("first graph rollback status=%+v", rolledBack)
	}
	if _, err := store.ActiveProjectReleaseSet(ctx, acct.ID, project.ID, "production"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("active graph after first-graph rollback = %v, want none", err)
	}
	if weighted, err := store.LiveDeploymentForScope(ctx, app.ID, "production"); err != nil || weighted.ID != previous.ID {
		t.Fatalf("first graph rollback changed weighted fallback: %+v err=%v", weighted, err)
	}
	if releaseID, deploymentID, err := store.ResolveProjectRelease(ctx, app.ID, "production", promoted.ReleaseGraph.TargetReleaseSetID); err != nil || releaseID != promoted.ReleaseGraph.TargetReleaseSetID || deploymentID != promoted.Workloads[0].TargetDeploymentID {
		t.Fatalf("old client lost its promoted graph after rollback: %q/%q err=%v", releaseID, deploymentID, err)
	}
}

func TestProjectEnvironmentPromotionSyncsConfigWithReleaseGraph(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	sourceConfigValues, sourceConfigHash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"region":"eu","replicas":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{
		AccountID: acct.ID, ProjectID: project.ID, EnvironmentSlug: "staging",
		ConfigHash: sourceConfigHash, Values: sourceConfigValues,
	}); err != nil {
		t.Fatal(err)
	}
	previousConfigValues, previousConfigHash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"region":"us","replicas":2}`))
	if err != nil {
		t.Fatal(err)
	}
	previousConfig, err := store.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{
		AccountID: acct.ID, ProjectID: project.ID, EnvironmentSlug: "production",
		ConfigHash: previousConfigHash, Values: previousConfigValues,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	createLive := func(environment, image, rootfs string) state.Deployment {
		t.Helper()
		deployment, err := store.CreateDeployment(ctx, state.Deployment{
			AppID: app.ID, Scope: environment, ImageDigest: image, Status: state.DeployPending,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/rootfs/"+rootfs, "apps/"+rootfs+".ext4", 42); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		return deployment
	}
	sourceGraphDeployment := createLive("staging", "sha256:source-graph", "source-graph")
	previousTarget := createLive("production", "sha256:production-old", "production-old")
	sourceGraph, err := store.PublishProjectReleaseSet(ctx, acct.ID, project.ID, "staging", 1800,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: sourceGraphDeployment.ID}})
	if err != nil {
		t.Fatal(err)
	}
	qualification := createProjectEnvironmentQualificationForTest(t, store, acct, project, "staging", sourceGraph.ID, "passed", "passed")
	// Newer direct releases must not replace the exact source/target members
	// selected by active graph pointers during preview.
	createLive("staging", "sha256:source-newer", "source-newer")
	previousTargetGraph, err := store.PublishProjectReleaseSet(ctx, acct.ID, project.ID, "production", 1800,
		[]state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: previousTarget.ID}})
	if err != nil {
		t.Fatal(err)
	}
	createLive("production", "sha256:production-newer", "production-newer")

	previewReq, previewRec := projectRequest(http.MethodGet, "/v1/projects/shop/environments/production/promotion-preview?from=staging&sync_config=true", "shop", nil)
	previewReq.SetPathValue("environment", "production")
	srv.previewProjectEnvironmentPromotion(previewRec, previewReq, acct)
	if previewRec.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", previewRec.Code, previewRec.Body.String())
	}
	var preview api.ProjectEnvironmentPromotionPreviewResponse
	if err := json.Unmarshal(previewRec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.CanPromote || !preview.SyncConfig || preview.FromReleaseSet == nil || preview.FromReleaseSet.ID != sourceGraph.ID ||
		preview.ToReleaseSet == nil || preview.ToReleaseSet.ID != previousTargetGraph.ID || len(preview.Changes) != 1 ||
		preview.Changes[0].SourceDeploymentID != sourceGraphDeployment.ID || preview.Changes[0].TargetDeploymentID != previousTarget.ID {
		t.Fatalf("preview did not preserve graph members: %+v", preview)
	}
	wire, err := decodeProjectEnvironmentPromotionToken(preview.PromotionToken)
	if err != nil || !wire.SyncConfig {
		t.Fatalf("promotion token sync_config=%t err=%v", wire.SyncConfig, err)
	}
	approvalToken, _, problem := srv.issueProjectEnvironmentPromotionApproval(ctx, acct, project.Slug, "production", preview.PromotionToken)
	if problem != nil {
		t.Fatalf("issue promotion approval: %v", problem)
	}
	body, err := json.Marshal(api.PromoteProjectEnvironmentRequest{
		FromEnvironment: "staging", PromotionToken: preview.PromotionToken, ApprovalToken: approvalToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promote", "shop", body)
	req.SetPathValue("environment", "production")
	req.Header.Set("Idempotency-Key", "promotion-graph-atomic")
	srv.promoteProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("promotion status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response api.ProjectEnvironmentPromotionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	promotion, workloads, err := store.ProjectEnvironmentPromotionByID(ctx, acct.ID, project.Slug, "production", response.PromotionID)
	if err != nil {
		t.Fatal(err)
	}
	if promotion.SourceReleaseSetID != sourceGraph.ID || promotion.SourceQualificationID != qualification.ID || promotion.PreviousTargetReleaseSetID != previousTargetGraph.ID ||
		promotion.TargetReleaseSetID == "" || promotion.TargetReleaseSetID == previousTargetGraph.ID || len(workloads) != 1 ||
		!promotion.SyncConfig || promotion.SourceConfigHash != sourceConfigHash ||
		promotion.PreviousTargetConfigHash != previousConfigHash || promotion.TargetConfigVersion <= previousConfig.Version {
		t.Fatalf("promotion graph checkpoints=%+v workloads=%+v", promotion, workloads)
	}
	activeConfig, err := store.ProjectEnvironmentConfigLatest(ctx, acct.ID, project.ID, "production")
	if err != nil || activeConfig.ConfigHash != sourceConfigHash || string(activeConfig.Values) != string(sourceConfigValues) {
		t.Fatalf("synced target config=%+v err=%v; want source hash %s", activeConfig, err, sourceConfigHash)
	}
	active, err := store.ActiveProjectReleaseSet(ctx, acct.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != promotion.TargetReleaseSetID || len(active.Members) != 1 || active.Members[0].DeploymentID != workloads[0].TargetDeploymentID {
		t.Fatalf("atomic target graph=%+v promotion=%+v workload=%+v", active, promotion, workloads[0])
	}

	rollbackReq, rollbackRec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promotions/"+response.PromotionID+"/rollback", "shop", nil)
	rollbackReq.SetPathValue("environment", "production")
	rollbackReq.SetPathValue("promotion", response.PromotionID)
	rollbackReq.Header.Set("Idempotency-Key", "rollback-graph-atomic")
	srv.rollbackProjectEnvironmentPromotion(rollbackRec, rollbackReq, acct)
	if rollbackRec.Code != http.StatusOK {
		t.Fatalf("graph rollback status=%d body=%s", rollbackRec.Code, rollbackRec.Body.String())
	}
	rolledBack, _, err := store.ProjectEnvironmentPromotionByID(ctx, acct.ID, project.Slug, "production", response.PromotionID)
	if err != nil {
		t.Fatal(err)
	}
	active, err = store.ActiveProjectReleaseSet(ctx, acct.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.RollbackReleaseSetID == "" || active.ID != rolledBack.RollbackReleaseSetID || active.ID == previousTargetGraph.ID ||
		len(active.Members) != 1 || active.Members[0].DeploymentID != previousTarget.ID ||
		rolledBack.RollbackConfigVersion <= promotion.TargetConfigVersion {
		t.Fatalf("rollback did not publish the previous graph as a new atomic release: promotion=%+v graph=%+v", rolledBack, active)
	}
	rolledBackConfig, err := store.ProjectEnvironmentConfigLatest(ctx, acct.ID, project.ID, "production")
	if err != nil || rolledBackConfig.ConfigHash != previousConfigHash || string(rolledBackConfig.Values) != string(previousConfigValues) {
		t.Fatalf("rollback config=%+v err=%v; want previous hash %s", rolledBackConfig, err, previousConfigHash)
	}
	releaseID, deploymentID, err := store.ResolveProjectRelease(ctx, app.ID, "production", "")
	if err != nil || releaseID != active.ID || deploymentID != previousTarget.ID {
		t.Fatalf("default production release after rollback=%q/%q err=%v", releaseID, deploymentID, err)
	}
}

func TestProjectEnvironmentPromotionHashIncludesReleaseSetIdentity(t *testing.T) {
	configDiff := api.ProjectEnvironmentConfigDiffResponse{FromHash: "from", ToHash: "to"}
	first, err := projectEnvironmentPromotionHash("shop", "staging", "production", configDiff,
		false, "release-source-a", "release-target", "qualification-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := projectEnvironmentPromotionHash("shop", "staging", "production", configDiff,
		false, "release-source-b", "release-target", "qualification-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("promotion identity did not change when the source release set changed")
	}
}

// Spec §4.7: past_due blocks deploys. A promotion writes new live
// deployments into the target environment, so it must be refused too.
func TestProjectEnvironmentPromotionBlockedWhilePastDue(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"staging", "production"} {
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope,
			SourceSHA256: "source-" + scope, Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, d.ID, "/rootfs/"+scope, "apps/"+scope+".ext4", 42); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.ListDeploymentsForApp(ctx, app.ID, 100, 0)
	if err != nil {
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
	body, err := json.Marshal(api.PromoteProjectEnvironmentRequest{
		FromEnvironment: "staging", PromotionToken: preview.PromotionToken, ApprovalToken: approvalToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	pastDue := acct
	pastDue.Status = state.AccountPastDue
	req, rec := projectRequest(http.MethodPost, "/v1/projects/shop/environments/production/promote", "shop", body)
	req.SetPathValue("environment", "production")
	req.Header.Set("Idempotency-Key", "promotion-past-due")
	srv.promoteProjectEnvironment(rec, req, pastDue)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("past_due promotion status=%d body=%s, want 402", rec.Code, rec.Body.String())
	}
	after, err := store.ListDeploymentsForApp(ctx, app.ID, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("past_due promotion created deployments: before=%d after=%d", len(before), len(after))
	}
}

func TestProjectEnvironmentPromotionHashIncludesQualificationIdentity(t *testing.T) {
	configDiff := api.ProjectEnvironmentConfigDiffResponse{FromHash: "from", ToHash: "to"}
	first, err := projectEnvironmentPromotionHash("shop", "staging", "production", configDiff,
		false, "release-source", "release-target", "qualification-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := projectEnvironmentPromotionHash("shop", "staging", "production", configDiff,
		false, "release-source", "release-target", "qualification-b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("promotion identity did not change when source qualification changed")
	}
}

func createProjectEnvironmentQualificationForTest(t *testing.T, store *state.MemStore, acct state.Account, project state.Project, environment, releaseSetID, health, smoke string) state.ProjectEnvironmentQualification {
	t.Helper()
	ctx := context.Background()
	configurationVersion := int64(0)
	configurationHash := api.EmptyProjectEnvironmentConfigHash()
	configuration, configErr := store.ProjectEnvironmentConfigLatest(ctx, acct.ID, project.ID, environment)
	if configErr == nil {
		configurationVersion, configurationHash = configuration.Version, configuration.ConfigHash
	} else if !errors.Is(configErr, state.ErrNotFound) {
		t.Fatal(configErr)
	}
	release, err := store.ProjectReleaseSetByID(ctx, acct.ID, project.ID, environment, releaseSetID)
	if err != nil {
		t.Fatal(err)
	}
	resultsForStatus := func(checkStatus string) []state.ProjectEnvironmentQualificationResult {
		results := make([]state.ProjectEnvironmentQualificationResult, 0, len(release.Members))
		for _, member := range release.Members {
			app, err := store.AppByID(ctx, member.AppID)
			if err != nil {
				t.Fatal(err)
			}
			result := state.ProjectEnvironmentQualificationResult{
				WorkloadSlug: app.Slug, DeploymentID: member.DeploymentID, Status: "passed",
			}
			status := 204
			if checkStatus == "failed" {
				result.Status = "failed"
				result.ErrorCode = "unexpected_status"
				status = 503
			}
			result.HTTPStatus = &status
			results = append(results, result)
		}
		return results
	}
	secretRevisionHashes := make(map[string]string, len(release.Members))
	for _, member := range release.Members {
		app, err := store.AppByID(ctx, member.AppID)
		if err != nil {
			t.Fatal(err)
		}
		secrets, err := store.ListAppSecretsInScope(ctx, acct.ID, app.ID, environment)
		if err != nil {
			t.Fatal(err)
		}
		revisions := make([]api.ProjectEnvironmentSecretRevision, 0, len(secrets))
		for _, secret := range secrets {
			managedBy, bindingID := projectEnvironmentSecretOwner(secret)
			revisions = append(revisions, api.ProjectEnvironmentSecretRevision{
				Key: secret.Key, Version: secret.SecretVersion, ManagedBy: managedBy,
				BindingID: bindingID, CredentialGeneration: secret.ManagedCredentialGeneration,
			})
		}
		hash, err := api.ProjectEnvironmentSecretRevisionHash(revisions)
		if err != nil {
			t.Fatal(err)
		}
		secretRevisionHashes[app.Slug] = hash
	}
	qualification, err := store.CreateProjectEnvironmentQualification(context.Background(), acct.ID, project.ID, environment, releaseSetID, configurationVersion, configurationHash,
		secretRevisionHashes,
		[]state.ProjectEnvironmentQualificationCheck{
			{Name: "health", Status: health, Results: resultsForStatus(health)},
			{Name: "smoke", Status: smoke, Results: resultsForStatus(smoke)},
		})
	if err != nil {
		t.Fatal(err)
	}
	return qualification
}
