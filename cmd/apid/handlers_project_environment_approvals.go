package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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
	if planToken == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Plan token required", "plan_token is required"))
		return
	}
	approvalToken, approval, problem := s.issueProjectEnvironmentApproval(r.Context(), acct, projectSlug, environment, planToken)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusCreated, api.ProjectEnvironmentApprovalResponse{
		ApprovalToken: approvalToken,
		Environment:   environment,
		ExpiresAt:     approval.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (s *server) issueProjectEnvironmentApproval(ctx context.Context, acct state.Account, projectSlug, environment, planToken string) (string, state.ProjectEnvironmentApproval, *api.Problem) {
	pt, err := decodePlanToken(planToken)
	if err != nil || pt.AccountID != acct.ID || pt.Slug != projectSlug || pt.Environment != environment {
		return "", state.ProjectEnvironmentApproval{}, api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
			"Plan token does not match", "approve the exact plan generated for this project and environment")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", state.ProjectEnvironmentApproval{}, api.ErrCapacity("could not create environment approval")
	}
	approvalToken := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now().UTC()
	approval, err := s.store.CreateProjectEnvironmentApproval(ctx, state.ProjectEnvironmentApproval{
		AccountID:         acct.ID,
		ProjectSlug:       projectSlug,
		EnvironmentSlug:   environment,
		PlanTokenHash:     hashProjectEnvironmentApprovalMaterial(planToken),
		ApprovalTokenHash: hashProjectEnvironmentApprovalMaterial(approvalToken),
		ExpiresAt:         now.Add(projectEnvironmentApprovalTTL),
		CreatedAt:         now,
	})
	if err != nil {
		return "", state.ProjectEnvironmentApproval{}, api.ErrCapacity("could not create environment approval")
	}
	s.audit.Emit(ctx, "project.environment.approved", &acct.ID, map[string]any{
		"project_slug":    projectSlug,
		"environment":     environment,
		"approval_id":     approval.ID,
		"plan_token_hash": approval.PlanTokenHash,
		"expires_at":      approval.ExpiresAt.UTC().Format(time.RFC3339),
	})
	return approvalToken, approval, nil
}

func hashProjectEnvironmentApprovalMaterial(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
