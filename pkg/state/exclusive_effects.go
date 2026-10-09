// adr: 488
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
)

var ErrOperationEffectDestination = fmt.Errorf("operation effect destination unavailable: %w", ErrInvalidArgument)

// Existing application outbox events may already use this event name. Only
// an immutable effect correlation opts a delivery into the operation guard.
var ErrNotOperationEffect = errors.New("delivery is not a managed operation effect")

// OperationEffectDeliveryStore rechecks persisted effect identity and current
// scope before each external attempt. An already started HTTP request cannot
// be recalled by a later suspension or receiver update.
// ErrNotOperationEffect preserves the existing ordinary application outbox.
type OperationEffectDeliveryStore interface {
	OperationEffectDeliveryAllowed(context.Context, string) (bool, error)
}

type exclusiveStoredEffect struct {
	Effect exclusivework.Effect
	Record api.OperationEffectRecord
}

func operationEffectDestinationMatches(op ExclusiveOperation, hook AppWebhook) bool {
	if !hook.Enabled || hook.AccountID != op.AccountID || !slices.Contains(hook.EventFilter, OperationEffectEvent) {
		return false
	}
	if op.PlatformTenantID != "" {
		return hook.Scope == AppWebhookScopePlatformTenant && hook.PlatformTenantID == op.PlatformTenantID
	}
	return (hook.Scope == "" || hook.Scope == AppWebhookScopeApp) && hook.AppID == op.AppID
}

func operationEffectBody(op ExclusiveOperation, effect exclusivework.Effect) ([]byte, error) {
	// Jobs and deployment tasks do not return this HTTP response contract.
	var request struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(op.Request, &request); err != nil || op.AppID == "" || op.JobID != "" || request.Kind != "" {
		return nil, ErrOperationEffectDestination
	}
	return json.Marshal(api.OperationEffectPayload{
		OperationID: op.ID, AppID: op.AppID, PlatformTenantID: op.PlatformTenantID,
		Generation: op.Generation, Name: effect.Name, Type: effect.Type, Data: effect.Payload,
	})
}
