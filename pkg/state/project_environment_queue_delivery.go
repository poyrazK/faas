package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// This private transport primitive consumes prepared stage queues. Public
// activation still requires the class-specific runtime and clone qualification.
type ProjectEnvironmentQueueDeliveryScope struct {
	AccountID    string
	ProjectID    string
	DeploymentID string
	BindingName  string
}

type ProjectEnvironmentQueueDeliveryRequest struct {
	ProjectEnvironmentQueueDeliveryScope
	Mode         string // The adapter must match the pinned consumer's transport.
	LeaseSeconds int
}

type ProjectEnvironmentQueueDelivery struct {
	Invocation Invocation
	Consumer   ProjectEnvironmentQueueConsumer
	Receipt    string // Returned once; never persisted in plaintext.
}

type ProjectEnvironmentQueueDeliveryStore interface {
	ClaimNextProjectEnvironmentQueueDelivery(context.Context, ProjectEnvironmentQueueDeliveryRequest) (ProjectEnvironmentQueueDelivery, error)
	CompleteProjectEnvironmentQueueDelivery(context.Context, ProjectEnvironmentQueueDeliveryScope, string, string, json.RawMessage) error
	RetryProjectEnvironmentQueueDelivery(context.Context, ProjectEnvironmentQueueDeliveryScope, string, string, string) error
}

type environmentQueueReceipt struct {
	Attempt        int
	TokenHash      string
	OwnerHash      string
	IssuedAt       time.Time
	LeaseExpiresAt time.Time
}

func validateEnvironmentQueueDeliveryScope(scope ProjectEnvironmentQueueDeliveryScope) error {
	if err := validateQueuePreparationIDs(scope.AccountID, scope.ProjectID, scope.DeploymentID); err != nil {
		return err
	}
	if !environmentQueueNameRE.MatchString(scope.BindingName) {
		return ErrInvalidArgument
	}
	return nil
}

func validateEnvironmentQueueDeliveryRequest(req ProjectEnvironmentQueueDeliveryRequest) error {
	if err := validateEnvironmentQueueDeliveryScope(req.ProjectEnvironmentQueueDeliveryScope); err != nil {
		return err
	}
	if (req.Mode != "push" && req.Mode != "pull") || req.LeaseSeconds <= 0 {
		return ErrInvalidArgument
	}
	return nil
}

func environmentQueueDeliveryLimits(account Account, leaseSeconds int) (api.Limits, error) {
	limits, ok := api.LimitsFor(account.Plan)
	if !ok {
		return limits, ErrInvalidArgument
	}
	if !account.Active() {
		return limits, ErrConflict
	}
	if limits.MaxQueueDepth <= 0 || limits.MaxAsyncInvocationsPerAccount <= 0 {
		return limits, ErrQuotaExceeded
	}
	if leaseSeconds > limits.MaxAsyncInvocationDeadlineSeconds {
		return limits, ErrInvalidArgument
	}
	return limits, nil
}

func newEnvironmentQueueReceipt() (string, string, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(secret[:])
	digest := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(digest[:]), nil
}

func environmentQueueReceiptTokenHash(id, token string) (string, error) {
	if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
		return "", ErrInvalidArgument
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != token {
		return "", ErrInvalidArgument
	}
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:]), nil
}

func environmentQueueReceiptOwnerHash(owner InvocationEnvironmentQueueAdmission) string {
	raw, _ := json.Marshal(owner)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func validateEnvironmentQueueReceipt(receipt environmentQueueReceipt, owner InvocationEnvironmentQueueAdmission, inv Invocation, tokenHash string, now time.Time) error {
	if subtle.ConstantTimeCompare([]byte(receipt.TokenHash), []byte(tokenHash)) != 1 ||
		receipt.OwnerHash != environmentQueueReceiptOwnerHash(owner) || receipt.Attempt != inv.Attempts ||
		inv.State != InvocationDispatching || !inv.QuotaReserved || inv.LeaseExpiresAt == nil || inv.ReceivedAt == nil ||
		!inv.ReceivedAt.Equal(receipt.IssuedAt) || !inv.LeaseExpiresAt.Equal(receipt.LeaseExpiresAt) ||
		!receipt.LeaseExpiresAt.After(now) || (inv.DeadlineAt != nil && !inv.DeadlineAt.After(now)) {
		return ErrNotFound
	}
	return nil
}

// The producer freezes the retry policy; a later account downgrade still
// clamps its attempt budget. Zero policy requests an immediate, finite retry.
func environmentQueueRetry(inv Invocation, plan api.Plan, now time.Time, lastError string) (Invocation, error) {
	limits, ok := api.LimitsFor(plan)
	if !ok {
		return inv, ErrInvalidArgument
	}
	policy := inv.RetryPolicy()
	inv.QuotaReserved, inv.LeaseExpiresAt, inv.InstanceID = false, nil, ""
	inv.LastError = lastError
	if environmentQueueDeliveryAttemptsExhausted(inv, limits) {
		inv.State, inv.CompletedAt = InvocationDeadLetter, &now
		outcome := OutcomeDeadLetter
		inv.Outcome = &outcome
	} else {
		inv.State, inv.Outcome, inv.CompletedAt = InvocationPending, nil, nil
		inv.DueAt = now.Add(policy.Backoff(inv.Attempts))
	}
	return inv, nil
}

func environmentQueueDeliveryAttemptsExhausted(inv Invocation, limits api.Limits) bool {
	return inv.Attempts >= api.EffectiveRetryMaxAttempts(inv.RetryPolicy().MaxAttempts, limits.MaxQueueAttempts)
}

func exhaustEnvironmentQueueDelivery(inv Invocation, now time.Time) Invocation {
	inv.State, inv.CompletedAt = InvocationDeadLetter, &now
	inv.LastError = "queue delivery attempt budget exhausted after lease recovery"
	inv.LeaseExpiresAt, inv.InstanceID = nil, ""
	outcome := OutcomeDeadLetter
	inv.Outcome = &outcome
	return inv
}

func cloneEnvironmentQueueDelivery(inv Invocation, consumer ProjectEnvironmentQueueConsumer, token string) ProjectEnvironmentQueueDelivery {
	consumer.RetryPolicyJSON = append(json.RawMessage(nil), consumer.RetryPolicyJSON...)
	return ProjectEnvironmentQueueDelivery{Invocation: cloneInvocationWorkEnvelope(inv), Consumer: consumer, Receipt: token}
}
