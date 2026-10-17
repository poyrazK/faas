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
	ProfileID           string   `json:"profile_id"`
	PushBranches        []string `json:"push_branches"`
	PullRequestBranches []string `json:"pull_request_branches,omitempty"`
	LatestOnly          bool     `json:"latest_only"`
	IncludePaths        []string `json:"include_paths,omitempty"`
	ExcludePaths        []string `json:"exclude_paths,omitempty"`
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
	Kind             string `json:"kind,omitempty"`
	PRNumber         int    `json:"pr_number,omitempty"`
}

type AutomationEventSummary struct {
	ID               string                  `json:"id"`
	PolicyID         string                  `json:"policy_id"`
	PolicyGeneration int64                   `json:"policy_generation"`
	ProfileID        string                  `json:"profile_id"`
	CommitSHA        string                  `json:"commit_sha"`
	Branch           string                  `json:"branch"`
	Kind             string                  `json:"kind"`
	PRNumber         int64                   `json:"pr_number,omitempty"`
	Status           string                  `json:"status"`
	Attempts         int                     `json:"attempts"`
	LastError        string                  `json:"last_error,omitempty"`
	ExecutionID      string                  `json:"execution_id,omitempty"`
	CreatedAt        time.Time               `json:"created_at"`
	RetryGeneration  int32                   `json:"retry_generation"`
	MaxRetries       int                     `json:"max_retry_generations"`
	Retryable        bool                    `json:"retryable"`
	NextAttemptAt    *time.Time              `json:"next_attempt_at,omitempty"`
	LeaseExpiresAt   *time.Time              `json:"lease_expires_at,omitempty"`
	LastRetry        *AutomationRetry        `json:"last_retry,omitempty"`
	SupersededBy     string                  `json:"superseded_by,omitempty"`
	SupersededSHA    string                  `json:"superseded_sha,omitempty"`
	SupersededAt     *time.Time              `json:"superseded_at,omitempty"`
	PathDecision     *AutomationPathDecision `json:"path_decision,omitempty"`
	CheckDelivery    *SourceCheckDelivery    `json:"check_delivery,omitempty"`
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
	if !ValidID(req.ProfileID) || len(req.PushBranches)+len(req.PullRequestBranches) == 0 {
		return req, ErrConflict
	}
	req.PushBranches = slices.Clone(req.PushBranches)
	req.PullRequestBranches = slices.Clone(req.PullRequestBranches)
	for _, branches := range [][]string{req.PushBranches, req.PullRequestBranches} {
		if len(branches) > api.CIGitHubMaxBranches {
			return req, ErrConflict
		}
		slices.Sort(branches)
		for i, branch := range branches {
			if !validGitHubBranch(branch) || i > 0 && branch == branches[i-1] {
				return req, ErrConflict
			}
		}
	}
	return normalizeAutomationPaths(req)
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
		for _, branch := range req.PullRequestBranches {
			if !slices.Contains(binding.PullRequestBranches, branch) {
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
	provenance := &AutomationProvenance{EventID: event.ID, PolicyID: event.Policy.ID, PolicyGeneration: event.Policy.Generation}
	if event.Source.Kind == "pull_request" {
		provenance.Kind, provenance.PRNumber = event.Source.Kind, event.Source.PRNumber
	}
	return provenance
}

func AdmitAutomationOnce(ctx context.Context, cfg Config, store AutomationStore, verify GitHubVerifier, heads ...AutomationHeadVerifier) (bool, error) {
	return AdmitAutomationWithResolvers(ctx, cfg, store, verify, automationBranchResolvers(heads))
}

func AdmitAutomationWithResolvers(ctx context.Context, cfg Config, store AutomationStore, verify GitHubVerifier, resolvers AutomationResolvers) (bool, error) {
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
		if event.Source.Kind == "pull_request" {
			req.automationPR, err = prepareAutomationPullRequest(ctx, store, verify, event, resolvers.PullRequest)
		} else {
			req.automationHead, err = prepareAutomationHead(ctx, store, verify, event, []AutomationHeadVerifier{resolvers.Branch})
		}
		if err == nil {
			req.automationPaths, err = prepareAutomationPaths(ctx, verify, event, req.automationPR, resolvers.Changes)
		}
		if err == nil {
			_, _, err = AdmitJobs(ctx, cfg, store, verify, account, "github-auto:"+event.ID, req)
		}
		if err == nil {
			return true, nil
		}
		if errors.Is(err, errAutomationDeferred) || errors.Is(err, ErrLeaseLost) {
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

func RunAutomationAdmissionWorker(ctx context.Context, cfg Config, store AutomationStore, verify GitHubVerifier, log *slog.Logger, heads ...AutomationHeadVerifier) {
	RunAutomationWorkerWithResolvers(ctx, cfg, store, verify, log, automationBranchResolvers(heads))
}

func RunAutomationWorkerWithResolvers(ctx context.Context, cfg Config, store AutomationStore, verify GitHubVerifier, log *slog.Logger, resolvers AutomationResolvers) {
	if !cfg.AutomationEnabled || !cfg.WorkerEnabled || !cfg.TransportEnabled || !cfg.DispatchEnabled {
		return
	}
	runGitHubWorker(ctx, func(ctx context.Context) (bool, error) {
		worked, err := AdmitAutomationWithResolvers(ctx, cfg, store, verify, resolvers)
		if err != nil {
			// Provider/DB error strings can contain connection details or URLs.
			return worked, errors.New("CI automation admission unavailable")
		}
		return worked, nil
	}, log)
}
