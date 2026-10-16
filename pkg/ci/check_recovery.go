package ci

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrCheckRetryConflict = errors.New("CI Check retry conflicts with delivery state or generation")

// CheckDelivery exposes bounded diagnostics without commands, credentials,
// provider responses, idempotency digests or publisher lease tokens.
type CheckDelivery struct {
	ExecutionID      string      `json:"execution_id"`
	Pipeline         string      `json:"pipeline"`
	CommitSHA        string      `json:"commit_sha"`
	ExecutionStatus  string      `json:"execution_status"`
	DeliveryStatus   string      `json:"delivery_status"`
	Attempts         int         `json:"attempts"`
	LastError        string      `json:"last_error,omitempty"`
	CheckID          int64       `json:"check_id,omitempty"`
	DesiredVersion   int64       `json:"desired_version"`
	DeliveredVersion int64       `json:"delivered_version"`
	VersionPending   bool        `json:"version_pending"`
	RetryGeneration  int32       `json:"retry_generation"`
	MaxRetries       int         `json:"max_retry_generations"`
	Retryable        bool        `json:"retryable"`
	RetryPending     bool        `json:"retry_pending"`
	NextAttemptAt    *time.Time  `json:"next_attempt_at,omitempty"`
	LeaseExpiresAt   *time.Time  `json:"lease_expires_at,omitempty"`
	LastRetry        *CheckRetry `json:"last_retry,omitempty"`
}

// CheckRetry is durable operator intent and an audit receipt. githubd alone
// sets AppliedAt when it resets the existing delivery's retry budget.
type CheckRetry struct {
	ID          string     `json:"id"`
	ExecutionID string     `json:"execution_id"`
	Generation  int32      `json:"generation"`
	RequestedAt time.Time  `json:"requested_at"`
	AppliedAt   *time.Time `json:"applied_at,omitempty"`
}

type CheckRetryRequest struct {
	ExpectedRetryGeneration int32 `json:"expected_retry_generation"`
}

type CheckRecoveryStore interface {
	Execution(context.Context, string, string) (Execution, error)
	CheckDeliveries(context.Context, string) ([]CheckDelivery, error)
	CheckDelivery(context.Context, string, string) (CheckDelivery, error)
	requestCheckRetry(context.Context, string, string, string, CheckRetryRequest) (CheckRetry, bool, error)
}

func checkDeliveryExhausted(status, reason string, attempts int) bool {
	return status == "dead" && attempts >= api.CIGitHubMaxAttempts &&
		(reason == "publish_failed" || reason == "unavailable" || reason == "attempts_exhausted")
}

// RequestCheckRetry only admits publication intent. It never calls a compute
// backend or the Checks API. The publisher rechecks source authority again.
func RequestCheckRetry(ctx context.Context, cfg Config, store CheckRecoveryStore, verify GitHubVerifier, account, execution, key string, req CheckRetryRequest) (CheckRetry, bool, error) {
	if !cfg.GitHubAllows(account) || !cfg.GitHubChecksEnabled || !cfg.ExecutionChecksEnabled || store == nil {
		return CheckRetry{}, false, ErrDispatchDisabled
	}
	if !ValidID(execution) || !ValidIdempotencyKey(key) || req.ExpectedRetryGeneration < 0 || req.ExpectedRetryGeneration > api.CICheckRetryMaxGenerations {
		return CheckRetry{}, false, ErrCheckRetryConflict
	}
	if verify == nil {
		return CheckRetry{}, false, ErrGitHubUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, api.CIGitHubRPCSeconds*time.Second)
	defer cancel()
	row, err := store.Execution(ctx, account, execution)
	if err != nil {
		return CheckRetry{}, false, err
	}
	if row.Mode != ExecutionJobs || row.Compute == nil {
		return CheckRetry{}, false, ErrNotFound
	}
	// A read does not manufacture delivery intent for executions admitted off.
	delivery, err := store.CheckDelivery(ctx, account, execution)
	if err != nil {
		return CheckRetry{}, false, err
	}
	if delivery.DeliveryStatus == "not_requested" {
		return CheckRetry{}, false, ErrCheckRetryConflict
	}
	b := row.Compute.Binding
	if err := verify(ctx, account, b.InstallationID, b.RepositoryID, b.Repository); err != nil {
		// Provider details can contain URLs or credentials.
		if errors.Is(err, ErrGitHubAccess) {
			return CheckRetry{}, false, ErrGitHubAccess
		}
		return CheckRetry{}, false, ErrGitHubUnavailable
	}
	return store.requestCheckRetry(ctx, account, execution, key, req)
}
