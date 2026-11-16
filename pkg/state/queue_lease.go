package state

import (
	"context"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// QueueLeaseRequest leases ready messages from the app's enabled production
// pull binding for QueueName (ADR-1140). InstanceID records the leasing
// instance when the caller presented a workload-identity token.
type QueueLeaseRequest struct {
	AccountID         string
	AppID             string
	QueueName         string
	MaxMessages       int
	VisibilitySeconds int
	InstanceID        string
}

// QueueLease is one leased message and the receipt that owns it.
type QueueLease struct {
	Invocation Invocation
	Receipt    string
}

// Receipt failure reasons. A receipt whose lease expired, was redelivered or
// was already settled is "lease_expired"; a malformed or foreign receipt is
// "invalid_receipt".
const (
	QueueLeaseReasonExpired = "lease_expired"
	QueueLeaseReasonInvalid = "invalid_receipt"
)

// QueueLeaseFailure is a receipt that was not applied.
type QueueLeaseFailure struct {
	Receipt string
	Reason  string
}

// QueueLeaseOutcome reports per-receipt results; one bad receipt never
// blocks the others.
type QueueLeaseOutcome struct {
	Succeeded []string
	Failed    []QueueLeaseFailure
}

// QueueLeaseStore is the production pull-lease primitive (ADR-1140).
type QueueLeaseStore interface {
	LeaseQueueMessages(ctx context.Context, req QueueLeaseRequest) ([]QueueLease, error)
	AckQueueLeases(ctx context.Context, accountID, appID, queueName string, receipts []string) (QueueLeaseOutcome, error)
	// NackQueueLeases applies the message's retry policy and attempt budget;
	// delay postpones redelivery beyond the policy's back-off.
	NackQueueLeases(ctx context.Context, accountID, appID, queueName string, receipts []string, delay time.Duration, lastError string) (QueueLeaseOutcome, error)
	ExtendQueueLeases(ctx context.Context, accountID, appID, queueName string, receipts []string, visibilitySeconds int) (QueueLeaseOutcome, error)
	// QueueLeasesByInstance counts unexpired leases per leasing instance.
	QueueLeasesByInstance(ctx context.Context, appID string) (map[string]int, error)
	// ReleaseQueueLeases returns leases whose consumer never received them
	// to pending with their unused attempt (ADR-20261112200000). A lease
	// that moved on since is left alone.
	ReleaseQueueLeases(ctx context.Context, appID string, leases []QueueLease) error
}

// queueLeaseLimits validates a lease request against the account's plan and
// returns the effective message count and visibility timeout.
func queueLeaseLimits(account Account, req QueueLeaseRequest) (int, int, error) {
	if !api.ValidQueueBindingName(req.QueueName) || req.MaxMessages < 0 || req.VisibilitySeconds < 0 {
		return 0, 0, ErrInvalidArgument
	}
	limits, ok := api.LimitsFor(account.Plan)
	if !ok {
		return 0, 0, ErrInvalidArgument
	}
	if !account.Active() {
		return 0, 0, ErrConflict
	}
	messages := req.MaxMessages
	if messages == 0 {
		messages = 1
	}
	visibility := req.VisibilitySeconds
	if visibility == 0 {
		visibility = api.QueueLeaseDefaultVisibilitySeconds
	}
	if messages > api.QueueLeaseMaxMessages || visibility > limits.MaxAsyncInvocationDeadlineSeconds {
		return 0, 0, ErrInvalidArgument
	}
	return messages, visibility, nil
}

// queueLeaseFits reports whether one more message stays within the lease
// response budget. The first message always fits: a single message is
// already bounded by the plan's per-message size.
func queueLeaseFits(leased, usedBytes int, inv Invocation) bool {
	return leased == 0 || usedBytes+queueLeaseBytes(inv) <= api.QueueLeaseMaxResponseBytes
}

func queueLeaseBytes(inv Invocation) int {
	return len(inv.Payload) + len(inv.Headers)
}

// newQueueLeaseReceipt returns the receipt handed to the consumer and the
// digest the store keeps.
func newQueueLeaseReceipt(invocationID string) (string, string, error) {
	token, digest, err := newEnvironmentQueueReceipt()
	if err != nil {
		return "", "", err
	}
	return invocationID + "." + token, storedCredentialHex(credTagQueueLeaseReceipt, digest), nil
}

// parseQueueLeaseReceipt splits a receipt into its message id and token digest.
func parseQueueLeaseReceipt(receipt string) (string, string, bool) {
	id, token, ok := strings.Cut(receipt, ".")
	if !ok {
		return "", "", false
	}
	digest, err := environmentQueueReceiptTokenHash(id, token)
	if err != nil {
		return "", "", false
	}
	return id, digest, true
}

type queueLeaseReceipt struct {
	Attempt          int
	ReplayGeneration int64
	TokenHash        string
	IssuedAt         time.Time
}

// queueLeaseExtension is how far an extend may move a lease issued at
// issuedAt: the requested visibility, but never past the plan's async
// deadline from the lease's issue. Consumers extend while their handler runs,
// so without the ceiling a hung handler held its message and a concurrency
// slot forever (queue review 2026-11-12, ADR-20261112200000). It reports
// false once the lease has used its whole lifetime.
func queueLeaseExtension(issuedAt, now time.Time, visibilitySeconds int, lifetime time.Duration) (int, bool) {
	if lifetime <= 0 || issuedAt.IsZero() {
		return visibilitySeconds, true
	}
	left := int(issuedAt.Add(lifetime).Sub(now) / time.Second)
	if left <= 0 {
		return 0, false
	}
	return min(visibilitySeconds, left), true
}

// queueLeaseOwned reports whether the receipt still owns the message: same
// app and queue, the current attempt and replay generation, and a lease that
// has not expired. The queue matches by the name the message was sent under,
// or by bindingID, the production binding the path's queue name names now: a
// lease selects a binding's messages by its identity, so messages sent before
// the binding's queue_name was renamed must settle under the new name too.
func queueLeaseOwned(inv Invocation, accountID, appID, queueName, bindingID string, r queueLeaseReceipt, digest string, now time.Time) bool {
	sameQueue := inv.QueueName == queueName || (bindingID != "" && inv.QueueBindingID == bindingID)
	return inv.AccountID == accountID && inv.AppID == appID && inv.Source == InvocationQueue && sameQueue &&
		credentialHexMatches(credTagQueueLeaseReceipt, digest, r.TokenHash) &&
		r.Attempt == inv.Attempts && r.ReplayGeneration == inv.ReplayGeneration &&
		inv.State == InvocationDispatching && inv.LeaseExpiresAt != nil && inv.LeaseExpiresAt.After(now)
}

// queueLeaseRetry is the nack disposition: the retry policy and attempt
// budget decide pending or dead-letter, and delay can only push the retry
// later.
func queueLeaseRetry(inv Invocation, plan api.Plan, now time.Time, delay time.Duration, lastError string) (Invocation, error) {
	inv, err := environmentQueueRetry(inv, plan, now, lastError)
	if err != nil {
		return inv, err
	}
	if inv.State == InvocationPending && now.Add(delay).After(inv.DueAt) {
		inv.DueAt = now.Add(delay)
	}
	return inv, nil
}
