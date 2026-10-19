// adr: 20261018210500
package state

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// operationQueueHold is the due time of a queued Operation's invocation or
// workflow run. Dispatchers never reach it; the releaser replaces it.
var operationQueueHold = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

const (
	operationQueueGroupRows   = 1000
	operationQueueUsageGroups = 200
)

var operationConcurrencyName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// OperationConcurrencyPolicyInput sets one policy on an app.
type OperationConcurrencyPolicyInput struct {
	AccountID string
	AppID     string
	Request   api.OperationConcurrencyPolicyRequest
	Now       time.Time
}

// OperationConcurrencyStore owns concurrency policies and the queue of held
// Operations.
type OperationConcurrencyStore interface {
	// PutOperationConcurrencyPolicy creates or replaces the policy for its
	// app, scope, Operation name and customer.
	PutOperationConcurrencyPolicy(ctx context.Context, in OperationConcurrencyPolicyInput) (api.OperationConcurrencyPolicy, bool, error)
	DeleteOperationConcurrencyPolicy(ctx context.Context, accountID, appID, id string) error
	OperationConcurrency(ctx context.Context, accountID, appID string) (api.OperationConcurrencyResponse, error)
	CustomerOperationQueue(ctx context.Context, accountID, tenantID string) ([]api.OperationQueueEntry, error)
	// ReleaseQueuedOperations starts queued Operations up to each customer's
	// free slots and returns how many it started.
	ReleaseQueuedOperations(ctx context.Context, now time.Time) (int, error)
	// OperationQueueStats reports the fleet's queued Operations and when the
	// oldest was queued (nil when none is).
	OperationQueueStats(ctx context.Context) (int64, *time.Time, error)
}

func validateOperationConcurrencyRequest(req api.OperationConcurrencyPolicyRequest) (scope string, err error) {
	scope = req.Scope
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	if api.ValidateScope(scope) != nil || !operationConcurrencyName.MatchString(req.Operation) ||
		req.MaxRunning < 1 || req.MaxRunning > api.OperationConcurrencyMaxRunning {
		return "", fmt.Errorf("%w: operation names an Operation, scope an environment, and max_running is 1 to %d", ErrInvalidArgument, api.OperationConcurrencyMaxRunning)
	}
	if req.PlatformTenantID != "" {
		if _, err := uuid.Parse(req.PlatformTenantID); err != nil {
			return "", ErrNotFound
		}
	}
	return scope, nil
}

// operationConcurrencyApplies reports whether a definition's Operations can
// be queued. A job run has no hold, so job definitions are never queued.
func operationConcurrencyApplies(def OperationDefinition) bool {
	return def.Spec.Job == "" && api.OperationConcurrencyEnabled(def.AccountID)
}

// operationConcurrencyHold reports whether a new Operation must queue: the
// customer has reached its limit, or already has work queued ahead of it.
func operationConcurrencyHold(limit int, limited bool, inFlight, queued int64) bool {
	return limited && (queued > 0 || inFlight >= int64(limit))
}

// operationQueueSlots is how many queued Operations one release may start.
func operationQueueSlots(limit int, limited bool, inFlight int64, queued int) int {
	if !limited {
		return queued
	}
	return max(0, min(queued, limit-int(inFlight)))
}

// holdOperationDispatch makes the Operation's backend wait for the releaser.
func holdOperationDispatch(op *Operation, inv *Invocation) {
	op.dispatchHold = operationQueueHold
	inv.DueAt = operationQueueHold
}

// operationDispatchAt is when a new Operation's workflow run becomes due.
func operationDispatchAt(op *Operation) time.Time {
	if !op.dispatchHold.IsZero() {
		return op.dispatchHold
	}
	return op.CreatedAt
}

func operationInFlight(state api.OperationState) bool {
	return state == api.OperationAccepted || state == api.OperationRunning || state == api.OperationRequiresReconciliation
}
