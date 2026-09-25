package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ReconcileGitHubProjectPreviewEnvironment is the APID-owned half of the
// githubd PR-preview bridge. Cloning here is important: APID prepares managed
// resource credentials and copies sealed customer secrets without exposing
// either to githubd or the webhook payload.
func (s *server) ReconcileGitHubProjectPreviewEnvironment(ctx context.Context, req githubdProjectPreviewRequest) (githubdProjectPreviewResult, error) {
	if req.AccountID == "" || req.InstallationID <= 0 || req.RepoFullName == "" || req.PRNumber <= 0 {
		return githubdProjectPreviewResult{}, state.ErrInvalidArgument
	}
	if req.Action != "opened" && req.Action != "synchronize" && req.Action != "reopened" && req.Action != "closed" {
		return githubdProjectPreviewResult{}, state.ErrInvalidArgument
	}
	if req.Action != "closed" && !isCanonicalCommitSHA(req.HeadSHA) {
		return githubdProjectPreviewResult{}, state.ErrInvalidArgument
	}
	project, err := s.store.ProjectByRepo(ctx, req.AccountID, req.InstallationID, req.RepoFullName)
	if errors.Is(err, state.ErrNotFound) {
		// A repository may still have legacy app bindings without a project
		// environment model. Treat that as an opt-out, not a retryable error.
		return githubdProjectPreviewResult{}, nil
	}
	if err != nil {
		return githubdProjectPreviewResult{}, fmt.Errorf("resolve project for repository: %w", err)
	}
	lifecycle, ok := s.store.(state.ProjectEnvironmentPreviewLifecycleStore)
	if !ok {
		return githubdProjectPreviewResult{}, errors.New("project environment preview lifecycle storage is unavailable")
	}
	if req.Action == "closed" {
		environment, lookupErr := s.store.ProjectEnvironmentByPreviewPR(ctx, req.AccountID, project.ID, req.PRNumber)
		if errors.Is(lookupErr, state.ErrNotFound) {
			return githubdProjectPreviewResult{}, nil
		}
		if lookupErr != nil {
			return githubdProjectPreviewResult{}, fmt.Errorf("find project PR preview environment: %w", lookupErr)
		}
		closed, closeErr := lifecycle.CloseProjectEnvironmentPreview(ctx, req.AccountID, project.ID, req.PRNumber, time.Now().UTC().Add(state.ProjectEnvironmentPreviewCloseGrace))
		if errors.Is(closeErr, state.ErrConflict) && environment.PreviewState == state.ProjectEnvironmentPreviewTearingDown {
			return githubdProjectPreviewResult{Reconciled: true, Environment: environment}, nil
		}
		if closeErr != nil {
			return githubdProjectPreviewResult{}, fmt.Errorf("close project PR preview environment: %w", closeErr)
		}
		return githubdProjectPreviewResult{Reconciled: true, Environment: closed}, nil
	}

	policyStore, ok := s.store.(state.GitHubDeployPolicyStore)
	if !ok {
		return githubdProjectPreviewResult{}, errors.New("GitHub deployment policy storage is unavailable")
	}
	policy, err := policyStore.GetGitHubDeployPolicy(ctx, project.ID, req.AccountID)
	if err != nil {
		return githubdProjectPreviewResult{}, fmt.Errorf("load GitHub deployment policy: %w", err)
	}
	if !policy.PreviewEnabled || policy.PreviewEnvironmentFrom == "" {
		return githubdProjectPreviewResult{}, nil
	}

	previewSlug := "pr-" + strconv.Itoa(req.PRNumber)
	if !api.ValidProjectEnvironmentSlug(previewSlug) || policy.PreviewEnvironmentFrom == previewSlug {
		return githubdProjectPreviewResult{}, fmt.Errorf("invalid PR preview environment identity %q from source %q", previewSlug, policy.PreviewEnvironmentFrom)
	}
	expiresAt := time.Now().UTC().Add(time.Duration(policy.PreviewTTLHours) * time.Hour)
	environment, lookupErr := s.store.ProjectEnvironmentByPreviewPR(ctx, req.AccountID, project.ID, req.PRNumber)
	if errors.Is(lookupErr, state.ErrNotFound) {
		acct, accountErr := s.store.AccountByID(ctx, req.AccountID)
		if accountErr != nil {
			return githubdProjectPreviewResult{}, fmt.Errorf("load account for project PR preview: %w", accountErr)
		}
		createReq := api.CreateProjectEnvironmentRequest{
			Slug: previewSlug, FromEnvironment: policy.PreviewEnvironmentFrom,
			PreviewPRNumber: req.PRNumber, PreviewHeadSHA: strings.ToLower(req.HeadSHA),
		}
		httpReq, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, "/internal/github/pr-preview", nil)
		if requestErr != nil {
			return githubdProjectPreviewResult{}, requestErr
		}
		environment, _, err = s.persistProjectEnvironment(httpReq, acct, project, createReq)
		if errors.Is(err, state.ErrConflict) {
			// Concurrent duplicate webhook delivery: the unique PR identity
			// decides the winner, then both deliveries refresh the same row.
			environment, err = s.store.ProjectEnvironmentByPreviewPR(ctx, req.AccountID, project.ID, req.PRNumber)
		}
		if err != nil {
			return githubdProjectPreviewResult{}, fmt.Errorf("clone project PR preview environment from %q: %w", policy.PreviewEnvironmentFrom, err)
		}
	} else if lookupErr != nil {
		return githubdProjectPreviewResult{}, fmt.Errorf("find project PR preview environment: %w", lookupErr)
	}
	if environment.Slug != previewSlug {
		return githubdProjectPreviewResult{}, fmt.Errorf("project PR preview %d is stored under unexpected environment %q", req.PRNumber, environment.Slug)
	}
	updated, err := lifecycle.UpdateProjectEnvironmentPreviewHead(ctx, req.AccountID, project.ID, req.PRNumber, strings.ToLower(req.HeadSHA), expiresAt)
	if err != nil {
		return githubdProjectPreviewResult{}, fmt.Errorf("refresh project PR preview environment: %w", err)
	}
	return githubdProjectPreviewResult{Reconciled: true, Environment: updated}, nil
}
