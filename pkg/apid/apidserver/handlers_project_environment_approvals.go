package apidserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const projectEnvironmentApprovalTTL = 15 * time.Minute

func (s *server) approveProjectEnvironment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	projectSlug := r.PathValue("slug")
	environment := r.PathValue("environment")
	protected, problem := s.projectDeploymentEnvironmentProtection(r.Context(), acct, projectSlug, environment)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if !protected {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
			"Environment is not protected", "approvals are only required for protected project environments"))
		return
	}
	var req api.CreateProjectEnvironmentApprovalRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	planToken := strings.TrimSpace(req.PlanToken)
	promotionToken := strings.TrimSpace(req.PromotionToken)
	if (planToken == "") == (promotionToken == "") {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Exactly one token required", "provide exactly one of plan_token or promotion_token"))
		return
	}
	if promotionToken != "" {
		if wire, err := decodeProjectEnvironmentPromotionToken(promotionToken); err == nil {
			if problem := validatePromotionSecretAccess(r, wire.ReleaseSecrets); problem != nil {
				api.WriteProblem(w, problem)
				return
			}
		}
		if problem := s.promotionApprovalByKeyProblem(r, acct, projectSlug, environment, promotionToken); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		approvalToken, approval, problem := s.issueProjectEnvironmentPromotionApproval(r.Context(), acct, projectSlug, environment, promotionToken)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		writeJSON(w, http.StatusCreated, projectEnvironmentApprovalResponse(approvalToken, approval))
		return
	}
	approvalToken, approval, problem := s.issueProjectEnvironmentApproval(r.Context(), acct, projectSlug, environment, planToken)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusCreated, projectEnvironmentApprovalResponse(approvalToken, approval))
}

func (s *server) getProjectEnvironmentApprovalStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	approval, err := s.store.ProjectEnvironmentApprovalByID(r.Context(), acct.ID, r.PathValue("slug"), r.PathValue("environment"), r.PathValue("approval"))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Environment approval not found", "no approval exists with that id"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not load environment approval"))
		return
	}
	now := time.Now().UTC()
	response := api.ProjectEnvironmentApprovalStatusResponse{
		ApprovalID:  approval.ID,
		Environment: approval.EnvironmentSlug,
		TokenKind:   approval.TokenKind,
		Status:      api.DeriveProjectEnvironmentApprovalStatus(approval.ConsumedAt, approval.ExpiresAt, now),
		CreatedAt:   approval.CreatedAt.UTC().Format(time.RFC3339),
		ExpiresAt:   approval.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if approval.ConsumedAt != nil {
		response.ConsumedAt = approval.ConsumedAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, response)
}

// promotionApprovalByKeyProblem refuses an API key or token's approval of a
// protected production promotion that stands in for an app's release
// approval policy (ADR-20261020085706 amendment 9). A person's approval is
// unaffected, and so is a key's when no promoted app has a policy.
func (s *server) promotionApprovalByKeyProblem(r *http.Request, acct state.Account, projectSlug, environment, promotionToken string) *api.Problem {
	if p, ok := principalFrom(r); !ok || p.Key == nil {
		return nil
	}
	plan, problem := s.validateProjectEnvironmentPromotionApproval(r.Context(), acct, projectSlug, environment, promotionToken)
	if problem != nil {
		return problem
	}
	return s.promotionApprovalKeyProblem(r, plan, environment)
}

func (s *server) issueProjectEnvironmentPromotionApproval(ctx context.Context, acct state.Account, projectSlug, environment, promotionToken string) (string, state.ProjectEnvironmentApproval, *api.Problem) {
	if _, problem := s.validateProjectEnvironmentPromotionApproval(ctx, acct, projectSlug, environment, promotionToken); problem != nil {
		return "", state.ProjectEnvironmentApproval{}, problem
	}
	return s.createProjectEnvironmentApproval(ctx, acct, projectSlug, environment, promotionToken, "promotion")
}

func (s *server) validateProjectEnvironmentPromotionApproval(ctx context.Context, acct state.Account, projectSlug, environment, promotionToken string) (projectEnvironmentPromotionPlan, *api.Problem) {
	wire, err := decodeProjectEnvironmentPromotionToken(promotionToken)
	if err != nil || wire.AccountID != acct.ID || wire.ProjectSlug != projectSlug || wire.ToEnvironment != environment {
		return projectEnvironmentPromotionPlan{}, api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
			"Promotion token does not match", "approve the exact promotion preview for this project and environment")
	}
	plan, problem := s.buildProjectEnvironmentPromotionSecretPlan(ctx, acct, projectSlug, wire.FromEnvironment, environment, wire.SyncConfig, wire.ReleaseSecrets, wire.Verify, wire.Migrate, wire.Workers)
	if problem != nil {
		return projectEnvironmentPromotionPlan{}, problem
	}
	if wire.ProjectID != plan.ProjectID || wire.SyncConfig != plan.SyncConfig || wire.FromConfigHash != plan.Preview.ConfigDiff.FromHash ||
		wire.ToConfigHash != plan.Preview.ConfigDiff.ToHash ||
		wire.SourceQualificationID != projectEnvironmentPromotionQualificationID(plan.Qualification) ||
		wire.PromotionHash != plan.Preview.PromotionHash {
		return projectEnvironmentPromotionPlan{}, api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
			"Promotion preview is stale", "the live releases or environment configuration changed; preview again")
	}
	if !plan.Preview.CanPromote {
		return projectEnvironmentPromotionPlan{}, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Promotion is blocked", strings.Join(plan.Preview.BlockingReasons, "; "))
	}
	if problem := projectEnvironmentPromotionExecutionProblem(plan); problem != nil {
		return projectEnvironmentPromotionPlan{}, problem
	}
	return plan, nil
}

func (s *server) issueProjectEnvironmentApproval(ctx context.Context, acct state.Account, projectSlug, environment, planToken string) (string, state.ProjectEnvironmentApproval, *api.Problem) {
	pt, err := decodePlanToken(planToken)
	if err != nil || pt.AccountID != acct.ID || pt.Slug != projectSlug || pt.Environment != environment || pt.Selection == nil {
		return "", state.ProjectEnvironmentApproval{}, api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
			"Plan token does not match", "approve the exact plan generated for this project and environment")
	}
	return s.createProjectEnvironmentApproval(ctx, acct, projectSlug, environment, planToken, "plan")
}

func (s *server) consumeProjectPlanApproval(ctx context.Context, acct state.Account, req *scanPlanRequest, planToken string) *api.Problem {
	approval, err := s.store.ConsumeProjectEnvironmentApproval(ctx, acct.ID, req.ProjectSlug, req.Environment,
		hashProjectEnvironmentApprovalMaterial(planToken), hashProjectEnvironmentApprovalMaterial(req.ApprovalToken), time.Now().UTC())
	if err != nil {
		return api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
			"Invalid environment approval", "the approval is expired or does not match this exact plan and environment")
	}
	s.audit.Emit(ctx, "project.environment.approval.consumed", &acct.ID, map[string]any{
		"project_slug": req.ProjectSlug,
		"environment":  req.Environment,
		"approval_id":  approval.ID,
		"token_kind":   approval.TokenKind,
		"consumed_at":  approval.ConsumedAt.UTC().Format(time.RFC3339),
	})
	return nil
}

func (s *server) createProjectEnvironmentApproval(ctx context.Context, acct state.Account, projectSlug, environment, token, tokenKind string) (string, state.ProjectEnvironmentApproval, *api.Problem) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", state.ProjectEnvironmentApproval{}, api.ErrCapacity("could not create environment approval")
	}
	approvalToken := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now().UTC()
	approver := acct.ID
	if actor, _, ok := middleware.AccountFromCtx(ctx); ok {
		approver = actor.ID
	}
	approval, err := s.store.CreateProjectEnvironmentApproval(ctx, state.ProjectEnvironmentApproval{
		ApprovedByAccountID: approver,
		AccountID:           acct.ID,
		ProjectSlug:         projectSlug,
		EnvironmentSlug:     environment,
		TokenKind:           tokenKind,
		PlanTokenHash:       hashProjectEnvironmentApprovalMaterial(token),
		ApprovalTokenHash:   hashProjectEnvironmentApprovalMaterial(approvalToken),
		ExpiresAt:           now.Add(projectEnvironmentApprovalTTL),
		CreatedAt:           now,
	})
	if err != nil {
		return "", state.ProjectEnvironmentApproval{}, api.ErrCapacity("could not create environment approval")
	}
	s.audit.Emit(ctx, "project.environment.approved", &acct.ID, map[string]any{
		"project_slug":    projectSlug,
		"environment":     environment,
		"approval_id":     approval.ID,
		"plan_token_hash": approval.PlanTokenHash,
		"token_kind":      tokenKind,
		"expires_at":      approval.ExpiresAt.UTC().Format(time.RFC3339),
	})
	return approvalToken, approval, nil
}

func hashProjectEnvironmentApprovalMaterial(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func projectEnvironmentApprovalResponse(approvalToken string, approval state.ProjectEnvironmentApproval) api.ProjectEnvironmentApprovalResponse {
	return api.ProjectEnvironmentApprovalResponse{
		ApprovalID:    approval.ID,
		ApprovalToken: approvalToken,
		Environment:   approval.EnvironmentSlug,
		TokenKind:     approval.TokenKind,
		Status:        api.DeriveProjectEnvironmentApprovalStatus(approval.ConsumedAt, approval.ExpiresAt, time.Now().UTC()),
		ExpiresAt:     approval.ExpiresAt.UTC().Format(time.RFC3339),
	}
}
