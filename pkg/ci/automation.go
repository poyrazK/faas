package ci

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type AutomationRequest struct {
	ProfileID    string   `json:"profile_id"`
	PushBranches []string `json:"push_branches"`
}

type AutomationPolicy struct {
	ID                string `json:"id"`
	AccountID         string `json:"account_id"`
	BindingID         string `json:"binding_id"`
	BindingGeneration int64  `json:"binding_generation"`
	AutomationRequest
	Enabled    bool      `json:"enabled"`
	Generation int64     `json:"generation"`
	CreatedAt  time.Time `json:"created_at"`
}

// AutomationEvent is authenticated source evidence, never a user command.
// Only githubd capture can insert it; apid owns execution admission.
type AutomationEvent struct {
	ID         string           `json:"id"`
	AccountID  string           `json:"account_id"`
	Policy     AutomationPolicy `json:"policy"`
	Binding    GitHubBinding    `json:"binding"`
	Source     GitHubSource     `json:"source"`
	Status     string           `json:"status,omitempty"`
	Attempts   int              `json:"attempts,omitempty"`
	LeaseToken string           `json:"-"`
}

type AutomationProvenance struct {
	EventID          string `json:"event_id"`
	PolicyID         string `json:"policy_id"`
	PolicyGeneration int64  `json:"policy_generation"`
}

type AutomationEventSummary struct {
	ID               string    `json:"id"`
	PolicyID         string    `json:"policy_id"`
	PolicyGeneration int64     `json:"policy_generation"`
	ProfileID        string    `json:"profile_id"`
	CommitSHA        string    `json:"commit_sha"`
	Branch           string    `json:"branch"`
	Status           string    `json:"status"`
	Attempts         int       `json:"attempts"`
	LastError        string    `json:"last_error,omitempty"`
	ExecutionID      string    `json:"execution_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type AutomationStore interface {
	JobsStore
	ProfileStore
	AutomationPolicies(context.Context, string) ([]AutomationPolicy, error)
	DisableAutomationPolicy(context.Context, string, string) (AutomationPolicy, error)
	AutomationEvents(context.Context, string, string) ([]AutomationEventSummary, error)
	ClaimAutomationEvent(context.Context, string) (AutomationEvent, error)
	FailAutomationEvent(context.Context, AutomationEvent, string, bool) error
	putAutomationPolicy(context.Context, string, GitHubBinding, AutomationRequest) (AutomationPolicy, error)
}

func NormalizeAutomationRequest(req AutomationRequest) (AutomationRequest, error) {
	if !ValidID(req.ProfileID) || len(req.PushBranches) < 1 || len(req.PushBranches) > api.CIGitHubMaxBranches {
		return req, ErrConflict
	}
	req.PushBranches = slices.Clone(req.PushBranches)
	slices.Sort(req.PushBranches)
	for i, branch := range req.PushBranches {
		if !validGitHubBranch(branch) || i > 0 && branch == req.PushBranches[i-1] {
			return req, ErrConflict
		}
	}
	return req, nil
}

// Saving policy needs source authority, but does not require or enable compute.
func SaveAutomationPolicy(ctx context.Context, cfg Config, store AutomationStore, verify GitHubVerifier, account, bindingID string, req AutomationRequest) (AutomationPolicy, error) {
	if !cfg.GitHubAllows(account) || store == nil {
		return AutomationPolicy{}, ErrDispatchDisabled
	}
	req, err := NormalizeAutomationRequest(req)
	if err != nil || !ValidID(bindingID) {
		return AutomationPolicy{}, ErrConflict
	}
	if verify == nil {
		return AutomationPolicy{}, ErrGitHubUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.CIGitHubRPCSeconds)*time.Second)
	defer cancel()
	profile, err := store.ExecutionProfile(ctx, account, req.ProfileID)
	if err != nil {
		return AutomationPolicy{}, err
	}
	if profile.State != ProfileActive || profile.BindingID != bindingID {
		return AutomationPolicy{}, ErrConflict
	}
	bindings, err := store.GitHubBindings(ctx, account)
	if err != nil {
		return AutomationPolicy{}, err
	}
	for _, binding := range bindings {
		if binding.ID != bindingID || !binding.Enabled || binding.Generation != profile.BindingGeneration {
			continue
		}
		for _, branch := range req.PushBranches {
			if !slices.Contains(binding.PushBranches, branch) {
				return AutomationPolicy{}, ErrConflict
			}
		}
		if err := verify(ctx, account, binding.InstallationID, binding.RepositoryID, binding.Repository); err != nil {
			return AutomationPolicy{}, err
		}
		return store.putAutomationPolicy(ctx, account, binding, req)
	}
	return AutomationPolicy{}, ErrNotFound
}

func automationProvenance(event *AutomationEvent) *AutomationProvenance {
	if event == nil {
		return nil
	}
	return &AutomationProvenance{EventID: event.ID, PolicyID: event.Policy.ID, PolicyGeneration: event.Policy.Generation}
}

func AdmitAutomationOnce(ctx context.Context, cfg Config, store AutomationStore, verify GitHubVerifier) (bool, error) {
	if store == nil || verify == nil {
		return false, nil
	}
	worked := false
	var failures error
	for _, account := range cfg.GitHubAccounts() {
		if !cfg.AutomationAllows(account) {
			continue
		}
		event, err := store.ClaimAutomationEvent(ctx, account)
		if errors.Is(err, ErrNoGitHubWork) {
			continue
		}
		if err != nil {
			return false, err
		}
		worked = true
		req := JobsSubmitRequest{ProfileID: event.Policy.ProfileID, SubmitRequest: SubmitRequest{CommitSHA: event.Source.CommitSHA}, automation: &event}
		_, _, err = AdmitJobs(ctx, cfg, store, verify, account, "github-auto:"+event.ID, req)
		if err == nil {
			return true, nil
		}
		if errors.Is(err, ErrLeaseLost) {
			continue
		}
		reason, permanent := "unavailable", false
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrGitHubAccess) || errors.Is(err, ErrGitHubIgnored) {
			reason, permanent = "authority_revoked", true
		} else if errors.Is(err, ErrLimit) {
			reason = "capacity"
		}
		saveErr := store.FailAutomationEvent(ctx, event, reason, permanent)
		if errors.Is(saveErr, ErrLeaseLost) {
			continue
		}
		if permanent {
			recordWorkerCycle(ctx, saveErr)
			failures = errors.Join(failures, saveErr)
			continue
		}
		// A full or unavailable account must not block admission for others.
		// Observe each bounded account decision: a long allowlist can keep this
		// sweep busy through many individually bounded GitHub calls.
		recordWorkerCycle(ctx, errors.Join(err, saveErr))
		failures = errors.Join(failures, err, saveErr)
	}
	return worked, failures
}

func RunAutomationAdmissionWorker(ctx context.Context, cfg Config, store AutomationStore, verify GitHubVerifier, log *slog.Logger) {
	if !cfg.AutomationEnabled || !cfg.WorkerEnabled || !cfg.TransportEnabled || !cfg.DispatchEnabled {
		return
	}
	runGitHubWorker(ctx, func(ctx context.Context) (bool, error) {
		worked, err := AdmitAutomationOnce(ctx, cfg, store, verify)
		if err != nil {
			// Provider/DB error strings can contain connection details or URLs.
			return worked, errors.New("CI automation admission unavailable")
		}
		return worked, nil
	}, log)
}
