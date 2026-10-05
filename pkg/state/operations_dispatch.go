package state

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Restore the ephemeral claim proof after the synthetic transport reloads the
// durable request. Claim capabilities deliberately never enter stored headers.
func admitOperationDispatch(ctx context.Context, store any, stored, wire Invocation) (Invocation, error) {
	if !InvocationHasOperation(stored) {
		return stored, nil
	}
	reader, ok := store.(interface {
		OperationByID(context.Context, string, string, string) (Operation, error)
	})
	if !ok {
		return Invocation{}, ErrNotFound
	}
	op, err := reader.OperationByID(ctx, stored.AccountID, stored.PlatformTenantID, stored.OperationID)
	if err != nil {
		return Invocation{}, err
	}
	var headers map[string]string
	if err := json.Unmarshal(wire.Headers, &headers); err != nil {
		return Invocation{}, ErrInvalidArgument
	}
	proof := map[string]string{}
	for name, value := range headers {
		key := strings.ToLower(name)
		if _, exists := proof[key]; exists {
			return Invocation{}, ErrInvalidArgument
		}
		proof[key] = value
	}
	if proof[strings.ToLower(api.OperationIDHeader)] != op.ID {
		return Invocation{}, ErrOperationStaleAttempt
	}
	attempt, err := strconv.Atoi(proof[strings.ToLower(api.OperationAttemptHeader)])
	if err != nil {
		return Invocation{}, ErrInvalidArgument
	}
	capability := proof[strings.ToLower(api.OperationCapabilityHeader)]
	authority := OperationExecutionAuthority{AccountID: stored.AccountID, AppID: stored.AppID, InstanceID: stored.InstanceID, InvocationID: stored.ID, Attempt: attempt, Capability: capability}
	if err := ValidateOperationExecutionAuthority(op, stored, authority, time.Now()); err != nil {
		return Invocation{}, err
	}
	return operationExecutionHeaders(stored, op, capability), nil
}
