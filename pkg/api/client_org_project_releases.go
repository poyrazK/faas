package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Organization forms of the atomic release operations. Each validates like its
// personal counterpart and addresses the project through its company URL.

func orgProjectBase(orgSlug, project string) string {
	return orgProjectPath(orgSlug) + "/" + url.PathEscape(project)
}

// GetOrgProjectSubmission reads a receipt for an attempt submitted to this company project.
func (c *Client) GetOrgProjectSubmission(ctx context.Context, orgSlug, project, attemptID string) (ProjectSubmissionReceipt, error) {
	var out ProjectSubmissionReceipt
	if !ValidProjectSlug(project) || !ValidDeploymentAttemptID(attemptID) {
		return out, fmt.Errorf("invalid project submission attempt ID")
	}
	return out, c.do(ctx, http.MethodGet, orgProjectBase(orgSlug, project)+"/submissions/"+attemptID, nil, &out)
}

// ControlOrgProjectRelease is the company form of ControlProjectRelease.
func (c *Client) ControlOrgProjectRelease(ctx context.Context, orgSlug, project, attemptID, action string, request ProjectReleaseControlRequest) (ProjectSubmissionReceipt, error) {
	var out ProjectSubmissionReceipt
	if !ValidProjectSlug(project) || !ValidDeploymentAttemptID(attemptID) || !ValidDeploymentAttemptID(request.RequestID) || request.ExpectedGeneration == 0 || (action != "abort" && action != "recover" && action != "contract") {
		return out, fmt.Errorf("invalid progressive deploy control request")
	}
	return out, c.do(ctx, http.MethodPost, orgProjectBase(orgSlug, project)+"/submissions/"+attemptID+"/"+action, request, &out)
}

// ResolveOrgProjectMigrationContract is the company form of ResolveProjectMigrationContract.
func (c *Client) ResolveOrgProjectMigrationContract(ctx context.Context, orgSlug, project, attempt string, request ProjectMigrationRecoveryRequest) (ProjectSubmissionReceipt, error) {
	var out ProjectSubmissionReceipt
	if !ValidProjectSlug(project) || !ValidDeploymentAttemptID(attempt) || attempt == request.ReplacementAttemptID || !request.Valid() {
		return out, fmt.Errorf("invalid migration recovery request")
	}
	return out, c.do(ctx, http.MethodPost, orgProjectBase(orgSlug, project)+"/submissions/"+attempt+"/resolve-contract", request, &out)
}

// StageOrgProjectReleaseSecrets is the company form of StageProjectReleaseSecrets.
func (c *Client) StageOrgProjectReleaseSecrets(ctx context.Context, orgSlug, project string, req ProjectReleaseSecretStageRequest) (ProjectReleaseSecretStageResponse, error) {
	var out ProjectReleaseSecretStageResponse
	if !ValidProjectSlug(project) || !ValidDeploymentAttemptID(req.RequestID) {
		return out, fmt.Errorf("invalid release secret staging identity")
	}
	return out, c.do(ctx, http.MethodPost, orgProjectBase(orgSlug, project)+"/release-secrets", req, &out)
}

// CheckOrgProjectDeployment is the company form of CheckProjectDeployment.
func (c *Client) CheckOrgProjectDeployment(ctx context.Context, orgSlug, project string, request ProjectDeploymentCheckRequest) (ProjectDeploymentReadiness, error) {
	return c.checkProjectDeployment(ctx, orgProjectBase(orgSlug, project)+"/deployment-readiness", project, request)
}

// PreviewOrgProjectEnvironmentPromotionSecrets is the company form of PreviewProjectEnvironmentPromotionSecrets.
func (c *Client) PreviewOrgProjectEnvironmentPromotionSecrets(ctx context.Context, orgSlug, project, target string, req ProjectEnvironmentPromotionSecretPreviewRequest) (ProjectEnvironmentPromotionPreviewResponse, error) {
	var out ProjectEnvironmentPromotionPreviewResponse
	if err := ValidatePromotionSecretReferences(req.Secrets); err != nil {
		return out, err
	}
	path := orgProjectBase(orgSlug, project) + "/environments/" + url.PathEscape(target) + "/promotion-preview"
	return out, c.do(ctx, http.MethodPost, path, req, &out)
}

// ControlOrgProjectEnvironmentPromotion is the company form of ControlProjectEnvironmentPromotion.
func (c *Client) ControlOrgProjectEnvironmentPromotion(ctx context.Context, orgSlug, project, environment, promotion, action string, request ProjectPromotionControlRequest) (ProjectPromotionControl, error) {
	var out ProjectPromotionControl
	if !ValidProjectSlug(project) || !ValidProjectEnvironmentSlug(environment) || promotion == "" || !request.Valid() || action != "abort" && action != "recover" {
		return out, fmt.Errorf("invalid promotion control request")
	}
	path := orgProjectBase(orgSlug, project) + "/environments/" + url.PathEscape(environment) + "/promotions/" + url.PathEscape(promotion) + "/" + action
	return out, c.do(ctx, http.MethodPost, path, request, &out)
}

// RecoverOrgProjectEnvironmentPromotionMigration is the company form of RecoverProjectEnvironmentPromotionMigration.
func (c *Client) RecoverOrgProjectEnvironmentPromotionMigration(ctx context.Context, orgSlug, project, environment, promotion string, request ProjectPromotionMigrationRecoveryRequest) (ProjectPromotionMigrationRecovery, error) {
	var out ProjectPromotionMigrationRecovery
	if !ValidProjectSlug(project) || !ValidProjectEnvironmentSlug(environment) || !request.Valid() {
		return out, fmt.Errorf("invalid promotion migration recovery request")
	}
	path := orgProjectBase(orgSlug, project) + "/environments/" + url.PathEscape(environment) + "/promotions/" + url.PathEscape(promotion) + "/recover-migration"
	return out, c.do(ctx, http.MethodPost, path, request, &out)
}
