package state

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
)

type workflowOperationStoredEffect struct {
	OperationID string
	Effect      exclusivework.Effect
	Record      api.OperationEffectRecord
}

func prepareManagedWorkflowStepCommit(in ManagedWorkflowStepCommit) (ManagedWorkflowStepCommit, error) {
	if in.RunID == "" || in.StepName == "" || in.Attempt < 1 || in.HTTPStatus < 200 || in.HTTPStatus >= 300 ||
		len(in.Output) == 0 || len(in.Output) > api.MaxExclusiveResultBytes || !json.Valid(in.Output) {
		return in, ErrInvalidArgument
	}
	runID, err := uuid.Parse(in.RunID)
	if err != nil {
		return in, ErrInvalidArgument
	}
	in.RunID = runID.String()
	operationID, err := uuid.Parse(in.OperationID)
	if err != nil {
		return in, ErrInvalidArgument
	}
	in.OperationID = operationID.String()
	expected, err := api.ManagedWorkflowStepOperationID(in.RunID, in.StepName)
	if err != nil || in.OperationID != expected {
		return in, ErrInvalidArgument
	}
	if len(in.Effects) > api.MaxExclusiveEffectsPerCommit {
		return in, ErrInvalidArgument
	}
	in.Output = slices.Clone(in.Output)
	in.Effects = slices.Clone(in.Effects)
	names := make(map[string]struct{}, len(in.Effects))
	for i := range in.Effects {
		effect := &in.Effects[i]
		if !exclusivework.NamePattern.MatchString(effect.Name) || len(effect.Payload) == 0 ||
			len(effect.Payload) > api.MaxExclusiveEffectPayloadBytes || !json.Valid(effect.Payload) ||
			len(effect.Type) == 0 || len(effect.Type) > api.MaxExclusiveEffectTypeBytes || !exclusivework.EffectTypePattern.MatchString(effect.Type) {
			return in, ErrInvalidArgument
		}
		if _, exists := names[effect.Name]; exists {
			return in, ErrInvalidArgument
		}
		names[effect.Name] = struct{}{}
		webhookID, err := uuid.Parse(effect.WebhookID)
		if err != nil {
			return in, ErrInvalidArgument
		}
		effect.WebhookID = webhookID.String()
		effect.Payload = slices.Clone(effect.Payload)
	}
	return in, nil
}

func workflowOperationEffectID(operationID, name string) string {
	namespace, _ := uuid.Parse(operationID)
	return uuid.NewSHA1(namespace, []byte("gregale-workflow-operation-effect-v1\n"+name)).String()
}

func workflowOperationEffectBody(operationID, appID, platformTenantID string, generation int64, effect exclusivework.Effect) ([]byte, error) {
	return json.Marshal(api.OperationEffectPayload{
		OperationID:      operationID,
		AppID:            appID,
		PlatformTenantID: platformTenantID,
		Generation:       generation,
		Name:             effect.Name,
		Type:             effect.Type,
		Data:             effect.Payload,
	})
}

func managedWorkflowEffectDestinationMatches(accountID, appID, platformTenantID string, hook AppWebhook) bool {
	if !hook.Enabled || canonicalMemUUID(hook.AccountID) != canonicalMemUUID(accountID) || !slices.Contains(hook.EventFilter, OperationEffectEvent) {
		return false
	}
	if platformTenantID != "" {
		return hook.Scope == AppWebhookScopePlatformTenant && canonicalMemUUID(hook.PlatformTenantID) == canonicalMemUUID(platformTenantID)
	}
	return (hook.Scope == "" || hook.Scope == AppWebhookScopeApp) && canonicalMemUUID(hook.AppID) == canonicalMemUUID(appID)
}

func workflowEffectStatusRecord(effect workflowOperationStoredEffect, delivery AppWebhookDelivery, deliveryExists, receiverExists bool) api.OperationEffectRecord {
	record := effect.Record
	if !deliveryExists || !receiverExists || delivery.WebhookID != record.WebhookID || delivery.Event != OperationEffectEvent {
		record.Status = "unavailable"
		return record
	}
	record.Status = string(delivery.Status)
	record.Attempt = delivery.Attempt
	record.LastError = delivery.LastError
	return record
}

func workflowEffectConflict(message string) error {
	return fmt.Errorf("%w: %s", ErrConflict, message)
}
