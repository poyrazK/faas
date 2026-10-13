package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// ErrQueueBindingEnvironmentUnavailable holds work until the original environment is restored.
var ErrQueueBindingEnvironmentUnavailable = fmt.Errorf("%w: captured queue binding environment is unavailable", ErrConflict)

// ErrQueueBindingRetired rejects new work or claims for a retained queue.
var ErrQueueBindingRetired = fmt.Errorf("%w: queue binding is retired", ErrConflict)

// QueueBindingConsumerStore publishes binding intent and its private consumer
// in one commit. A failed projection leaves both previous intents intact.
type QueueBindingConsumerStore interface {
	CreateQueueBindingWithConsumer(context.Context, QueueBinding) (QueueBindingConsumerResult, error)
	UpdateQueueBindingWithConsumer(context.Context, string, string, string, UpdateQueueBindingParams) (QueueBindingConsumerResult, error)
	DeleteQueueBindingWithConsumer(context.Context, string, string, string) (QueueBindingConsumerResult, error)
}

// QueueBindingHistoryStore exposes retained identity to reconciliation and
// recovery. Customer CRUD reads return only active bindings.
type QueueBindingHistoryStore interface {
	QueueBindingHistoryByID(context.Context, string, string, string) (QueueBinding, error)
	ListQueueBindingHistoryForApp(context.Context, string, string) ([]QueueBinding, error)
}

type QueueBindingConsumerResult struct {
	Binding                QueueBinding
	Changes                []QueueConsumerChange
	NotificationsCommitted bool
}

type QueueConsumerChange struct {
	Kind      string `json:"kind"`
	AppID     string `json:"app_id"`
	TriggerID string `json:"trigger_id"`
}

func applyQueueBindingPatch(row QueueBinding, p UpdateQueueBindingParams) QueueBinding {
	if p.QueueName != nil {
		row.QueueName = *p.QueueName
	}
	if p.Mode != nil {
		row.Mode = *p.Mode
	}
	if p.WorkloadClass != nil {
		row.WorkloadClass = *p.WorkloadClass
	}
	if p.Enabled != nil {
		row.Enabled = *p.Enabled
	}
	if p.MaxConcurrency != nil {
		row.MaxConcurrency = *p.MaxConcurrency
	}
	if p.RetryPolicyJSON != nil {
		row.RetryPolicyJSON = append([]byte(nil), (*p.RetryPolicyJSON)...)
	}
	if len(row.RetryPolicyJSON) == 0 {
		row.RetryPolicyJSON = []byte(`{}`)
	}
	return row
}

// QueueBindingAppClass is the app's workload class for queue binding checks.
// An explicit worker or job execution mode is customer intent and outranks
// the runtime-observed class, as it already does for queue_depth scaling
// targets; otherwise an app switched to worker mode through PATCH could keep
// its queue policy but never bind a queue.
func QueueBindingAppClass(observed WorkloadClass, executionMode string) WorkloadClass {
	switch executionMode {
	case api.ExecutionModeWorker:
		return WorkloadClassWorker
	case api.ExecutionModeJob:
		return WorkloadClassJob
	}
	return observed
}

func validateQueueBindingConsumer(row QueueBinding, appType AppType, appClass WorkloadClass) error {
	if row.RetiredAt != nil || row.DeploymentScope != "" && !api.ValidProjectEnvironmentSlug(row.DeploymentScope) {
		return ErrInvalidArgument
	}
	if !api.ValidQueueBindingName(row.Name) || !api.ValidQueueBindingName(row.QueueName) ||
		(row.Mode != "push" && row.Mode != "pull") || row.MaxConcurrency < 1 || row.MaxConcurrency > api.QueueBindingMaxConcurrency {
		return ErrInvalidArgument
	}
	if row.WorkloadClass == WorkloadClassHTTP {
		if row.Mode != "push" || appType != AppTypeFunction {
			return ErrInvalidArgument
		}
	} else if row.WorkloadClass != WorkloadClassWorker && row.WorkloadClass != WorkloadClassJob {
		return ErrInvalidArgument
	}
	if appClass != "" && appClass != row.WorkloadClass {
		return ErrInvalidArgument
	}
	var policy api.RetryPolicyDTO
	if len(row.RetryPolicyJSON) > 0 {
		var object map[string]json.RawMessage
		if json.Unmarshal(row.RetryPolicyJSON, &object) != nil || object == nil || json.Unmarshal(row.RetryPolicyJSON, &policy) != nil ||
			policy.Validate() != nil || policy.BaseSeconds > api.QueueBindingRetryMaxBaseSeconds || policy.MaxSeconds > api.QueueBindingRetryMaxSeconds {
			return ErrInvalidArgument
		}
	}
	return nil
}

type queueConsumerDefinition struct {
	Config                                          []byte
	BatchSize, BatchWindow, MaxAttempts, PayloadMax int32
}

func queueConsumerForBinding(row QueueBinding, limits api.Limits) (queueConsumerDefinition, error) {
	if !limits.TriggersAllowed || limits.TriggerLimitPerApp <= 0 || limits.TriggerLimitPerAccount <= 0 {
		return queueConsumerDefinition{}, ErrInvalidArgument
	}
	var policy api.RetryPolicyDTO
	if len(row.RetryPolicyJSON) > 0 && json.Unmarshal(row.RetryPolicyJSON, &policy) != nil {
		return queueConsumerDefinition{}, ErrInvalidArgument
	}
	config := map[string]any{"mode": "queue", "queue_binding_id": row.ID}
	if policy.MaxAttempts > 0 || policy.BaseSeconds > 0 || policy.MaxSeconds > 0 || policy.JitterSeconds > 0 {
		config["retry_policy"] = policy
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return queueConsumerDefinition{}, err
	}
	capValue := func(value, limit int) int32 {
		if limit > 0 && value > limit {
			value = limit
		}
		return int32(value)
	}
	attempts := 5
	if policy.MaxAttempts > 0 {
		attempts = policy.MaxAttempts
	}
	return queueConsumerDefinition{Config: encoded,
		BatchSize:   capValue(row.MaxConcurrency, limits.TriggerBatchSizeMax),
		BatchWindow: capValue(1000, limits.TriggerBatchWindowMaxSec*1000),
		MaxAttempts: capValue(attempts, limits.TriggerMaxAttemptsMax),
		PayloadMax:  int32(limits.TriggerPayloadMaxBytes)}, nil
}

func queueConsumerBindingID(config []byte) string {
	var marker struct {
		ID string `json:"queue_binding_id"`
	}
	if json.Unmarshal(config, &marker) != nil {
		return ""
	}
	return marker.ID
}

func queueConsumerMarkerPresent(config []byte) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(config, &object) != nil {
		return false
	}
	_, present := object["queue_binding_id"]
	return present
}
