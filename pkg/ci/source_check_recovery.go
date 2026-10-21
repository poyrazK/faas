package ci

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// SourceCheckDelivery describes only feedback for an immutable skipped source.
// It exposes no commands, provider details, request digests or lease tokens.
type SourceCheckDelivery struct {
	EventID         string            `json:"event_id"`
	BindingID       string            `json:"binding_id"`
	Pipeline        string            `json:"pipeline"`
	CommitSHA       string            `json:"commit_sha"`
	SourceStatus    string            `json:"source_status"`
	DeliveryStatus  string            `json:"delivery_status"`
	Attempts        int               `json:"attempts"`
	LastError       string            `json:"last_error,omitempty"`
	CheckID         int64             `json:"check_id,omitempty"`
	RetryGeneration int32             `json:"retry_generation"`
	MaxRetries      int               `json:"max_retry_generations"`
	Retryable       bool              `json:"retryable"`
	RetryPending    bool              `json:"retry_pending"`
	NextAttemptAt   *time.Time        `json:"next_attempt_at,omitempty"`
	LeaseExpiresAt  *time.Time        `json:"lease_expires_at,omitempty"`
	LastRetry       *SourceCheckRetry `json:"last_retry,omitempty"`
}

type SourceCheckRetry struct {
	ID          string     `json:"id"`
	EventID     string     `json:"event_id"`
	Generation  int32      `json:"generation"`
	RequestedAt time.Time  `json:"requested_at"`
	AppliedAt   *time.Time `json:"applied_at,omitempty"`
}

type SourceCheckRecoveryStore interface {
	SourceCheckDeliveries(context.Context, string, string) ([]SourceCheckDelivery, error)
	SourceCheckDelivery(context.Context, string, string, string) (SourceCheckDelivery, error)
	sourceCheckRecoverySource(context.Context, string, string, string) (AutomationEvent, error)
	requestSourceCheckRetry(context.Context, string, string, string, string, CheckRetryRequest) (SourceCheckRetry, bool, error)
}

func RequestSourceCheckRetry(ctx context.Context, cfg Config, store SourceCheckRecoveryStore, verify GitHubVerifier, account, binding, event, key string, req CheckRetryRequest) (SourceCheckRetry, bool, error) {
	if !cfg.GitHubAllows(account) || !cfg.GitHubChecksEnabled || !cfg.ExecutionChecksEnabled || store == nil {
		return SourceCheckRetry{}, false, ErrDispatchDisabled
	}
	if !ValidID(binding) || !ValidID(event) || !ValidIdempotencyKey(key) || req.ExpectedRetryGeneration < 0 || req.ExpectedRetryGeneration > api.CICheckRetryMaxGenerations {
		return SourceCheckRetry{}, false, ErrCheckRetryConflict
	}
	if verify == nil {
		return SourceCheckRetry{}, false, ErrGitHubUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, api.CIGitHubRPCSeconds*time.Second)
	defer cancel()
	source, err := store.sourceCheckRecoverySource(ctx, account, binding, event)
	if err != nil {
		return SourceCheckRetry{}, false, err
	}
	b := source.Binding
	if err := verify(ctx, account, b.InstallationID, b.RepositoryID, b.Repository); err != nil {
		if errors.Is(err, ErrGitHubAccess) {
			return SourceCheckRetry{}, false, ErrGitHubAccess
		}
		return SourceCheckRetry{}, false, ErrGitHubUnavailable
	}
	return store.requestSourceCheckRetry(ctx, account, binding, event, key, req)
}
