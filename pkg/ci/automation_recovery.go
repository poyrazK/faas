package ci

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrAutomationRetryConflict = errors.New("CI automation retry conflicts with source authority, state or generation")

// AutomationRetry records an explicit recovery of admission, never a command
// rerun. The source event and its original execution identity are preserved.
type AutomationRetry struct {
	ID               string    `json:"id"`
	EventID          string    `json:"event_id"`
	Generation       int32     `json:"generation"`
	PreviousAttempts int       `json:"previous_attempts"`
	PreviousError    string    `json:"previous_error"`
	RequestedAt      time.Time `json:"requested_at"`
}

type AutomationRetryRequest struct {
	ExpectedRetryGeneration int32 `json:"expected_retry_generation"`
}

type AutomationRecoveryStore interface {
	AutomationEventStatus(context.Context, string, string, string) (AutomationEventSummary, error)
	automationRetrySource(context.Context, string, string, string) (AutomationEvent, error)
	requestAutomationRetry(context.Context, string, string, string, string, AutomationRetryRequest) (AutomationRetry, bool, error)
}

func automationAdmissionExhausted(status, reason string, attempts int) bool {
	return status == "dead" && attempts >= api.CIGitHubMaxAttempts && (reason == "unavailable" || reason == "capacity")
}

// RequestAutomationRetry only resets bounded admission intent. The existing
// worker rechecks every execution gate and authority before it admits work.
func RequestAutomationRetry(ctx context.Context, cfg Config, store AutomationRecoveryStore, verify GitHubVerifier, account, binding, event, key string, req AutomationRetryRequest) (AutomationRetry, bool, error) {
	if !cfg.GitHubAllows(account) || !cfg.AutomationEnabled || store == nil {
		return AutomationRetry{}, false, ErrDispatchDisabled
	}
	if !ValidID(binding) || !ValidID(event) || !ValidIdempotencyKey(key) || req.ExpectedRetryGeneration < 0 || req.ExpectedRetryGeneration > api.CIAutomationRetryMaxGenerations {
		return AutomationRetry{}, false, ErrAutomationRetryConflict
	}
	if verify == nil {
		return AutomationRetry{}, false, ErrGitHubUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, api.CIGitHubRPCSeconds*time.Second)
	defer cancel()
	source, err := store.automationRetrySource(ctx, account, binding, event)
	if err != nil {
		return AutomationRetry{}, false, err
	}
	b := source.Binding
	if err := verify(ctx, account, b.InstallationID, b.RepositoryID, b.Repository); err != nil {
		if errors.Is(err, ErrGitHubAccess) {
			return AutomationRetry{}, false, ErrGitHubAccess
		}
		return AutomationRetry{}, false, ErrGitHubUnavailable
	}
	return store.requestAutomationRetry(ctx, account, binding, event, key, req)
}
