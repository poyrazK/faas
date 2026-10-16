package ci

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	ProfileActive   = "active"
	ProfileDisabled = "disabled"
)

// ExecutionProfile contains public intent only. Materialized image locations
// and full binding authority stay in the private database snapshot.
type ExecutionProfile struct {
	ID                string            `json:"id"`
	AccountID         string            `json:"account_id"`
	Name              string            `json:"name"`
	RevisionID        string            `json:"revision_id"`
	RevisionDigest    string            `json:"revision_digest"`
	BindingID         string            `json:"binding_id"`
	BindingGeneration int64             `json:"binding_generation"`
	Templates         map[string]string `json:"templates"`
	State             string            `json:"state"`
	CreatedAt         time.Time         `json:"created_at"`
}

type ExecutionProfileRequest struct {
	RevisionID string            `json:"revision_id"`
	BindingID  string            `json:"binding_id"`
	Templates  map[string]string `json:"templates"`
}

type ProfileStore interface {
	GitHubBindings(context.Context, string) ([]GitHubBinding, error)
	ExecutionProfile(context.Context, string, string) (ExecutionProfile, error)
	ExecutionProfiles(context.Context, string) ([]ExecutionProfile, error)
	DisableExecutionProfile(context.Context, string, string) (ExecutionProfile, error)
	putExecutionProfile(context.Context, string, string, ExecutionProfileRequest, GitHubBinding) (ExecutionProfile, error)
}

func ValidExecutionProfileRequest(req ExecutionProfileRequest) bool {
	if !ValidID(req.RevisionID) || !ValidID(req.BindingID) || len(req.Templates) < 1 || len(req.Templates) > api.CIMaxJobs {
		return false
	}
	for name, id := range req.Templates {
		if !ValidName(name) || !ValidID(id) {
			return false
		}
	}
	return true
}

// SaveExecutionProfile verifies source authority but never admits execution.
// Compute, storage, worker and dispatch may remain disabled during setup.
func SaveExecutionProfile(ctx context.Context, cfg Config, store ProfileStore, verify GitHubVerifier, account, name string, req ExecutionProfileRequest) (ExecutionProfile, error) {
	if !cfg.GitHubAllows(account) || store == nil {
		return ExecutionProfile{}, ErrDispatchDisabled
	}
	if !ValidName(name) || !ValidExecutionProfileRequest(req) {
		return ExecutionProfile{}, ErrConflict
	}
	if verify == nil {
		return ExecutionProfile{}, ErrGitHubUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.CIGitHubRPCSeconds)*time.Second)
	defer cancel()
	bindings, err := store.GitHubBindings(ctx, account)
	if err != nil {
		return ExecutionProfile{}, err
	}
	for _, binding := range bindings {
		if binding.ID != req.BindingID || !binding.Enabled || binding.RevisionID != req.RevisionID {
			continue
		}
		if err := verify(ctx, account, binding.InstallationID, binding.RepositoryID, binding.Repository); err != nil {
			return ExecutionProfile{}, err
		}
		return store.putExecutionProfile(ctx, account, name, req, binding)
	}
	return ExecutionProfile{}, ErrNotFound
}

func resolveProfileSubmission(ctx context.Context, store JobsStore, account string, req JobsSubmitRequest) (JobsSubmitRequest, error) {
	if req.ProfileID == "" {
		return req, nil
	}
	if !ValidID(req.ProfileID) || req.RevisionID != "" || req.BindingID != "" || len(req.Templates) != 0 {
		return JobsSubmitRequest{}, ErrConflict
	}
	profiles, ok := store.(ProfileStore)
	if !ok {
		return JobsSubmitRequest{}, ErrDispatchDisabled
	}
	profile, err := profiles.ExecutionProfile(ctx, account, req.ProfileID)
	if err != nil {
		return JobsSubmitRequest{}, err
	}
	if profile.State != ProfileActive {
		return JobsSubmitRequest{}, ErrConflict
	}
	req.RevisionID, req.BindingID, req.Templates = profile.RevisionID, profile.BindingID, profile.Templates
	return req, nil
}
