// adr:1352 — queue evidence comes from exact, fenced handler completion.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type QueueBindingCanaryStore interface {
	CreateQueueBindingCanary(context.Context, string, string, api.CreateQueueBindingCanaryRequest) (api.QueueBindingCanary, error)
	GetQueueBindingCanary(context.Context, string, string, string) (api.QueueBindingCanary, error)
	CancelQueueBindingCanary(context.Context, string, string, string) (api.QueueBindingCanary, error)
	LatestQueueBindingCanary(context.Context, string, string, string, string) (api.QueueBindingCanary, error)
}

type queueCanarySnapshot struct {
	MaxAttempts, PayloadMaxBytes                                              int
	FilterCriteria                                                            json.RawMessage
	PoisonStrategy                                                            string
	RevisionTTLSeconds                                                        int
	BindingID, Name, QueueName, Scope, EnvironmentID, BindingScope, TriggerID string
	Config                                                                    json.RawMessage
	BatchSize, BatchWindow, Concurrency                                       int
	Retry                                                                     json.RawMessage
	DeploymentID, RootfsKey, ImageDigest                                      string
	OverrideEnv                                                               json.RawMessage
}
type queueCanaryRecord struct {
	Report                api.QueueBindingCanary
	Snapshot              queueCanarySnapshot
	Cancelled             bool
	CompletedDeploymentID string
	CompletedAttempt      int
	CompletedGeneration   int64
}

func validateQueueCanaryRequest(req api.CreateQueueBindingCanaryRequest) error {
	if !canonicalStateUUID(req.DeploymentID) || !api.ValidQueueBindingName(req.Binding) || !req.PublishConsent || req.TTLSeconds < 1 || req.TTLSeconds > api.QueueCanaryMaxTTLSeconds {
		return ErrInvalidArgument
	}
	return nil
}
func queueCanarySupported(s queueCanarySnapshot) bool {
	var env map[string]string
	var bindings []string
	return s.RootfsKey != "" && s.ImageDigest != "" && json.Unmarshal(s.OverrideEnv, &env) == nil &&
		json.Unmarshal([]byte(env[api.QueueCanaryCapabilityEnv]), &bindings) == nil && slices.Contains(bindings, s.Name)
}
func newQueueCanary(s queueCanarySnapshot, req api.CreateQueueBindingCanaryRequest, accountID, appID string) (queueCanaryRecord, Invocation) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	id := uuid.NewString()
	expires := now.Add(time.Duration(req.TTLSeconds) * time.Second)
	headers, _ := json.Marshal(map[string]string{api.RevisionHeader: s.DeploymentID})
	payload, _ := json.Marshal(map[string]string{"gregale_queue_canary": "v1", "canary_id": id, "binding": s.Name})
	r := queueCanaryRecord{Report: api.QueueBindingCanary{ID: id, Binding: s.Name, DeploymentID: s.DeploymentID, Scope: s.Scope, Status: "published", PublishedAt: now, ExpiresAt: expires}, Snapshot: s}
	inv := Invocation{ID: id, AppID: appID, AccountID: accountID, Source: InvocationQueue, QueueBindingID: s.BindingID, QueueName: s.QueueName, DeploymentScope: s.Scope,
		State: InvocationPending, Method: "POST", Path: "/", Payload: payload, Headers: headers, DueAt: now, CreatedAt: now, DeadlineAt: &expires, RetryPolicyJSON: json.RawMessage(`{"max_attempts":1}`)}
	return r, inv
}
func sameQueueCanarySnapshot(a, b queueCanarySnapshot) bool {
	// jsonb normalization and map serialization must not create false changes.
	normalize := func(s queueCanarySnapshot) any {
		raw, _ := json.Marshal(s)
		var v any
		_ = json.Unmarshal(raw, &v)
		return v
	}
	return reflect.DeepEqual(normalize(a), normalize(b))
}
func queueCanaryStatus(r queueCanaryRecord, inv Invocation, current *queueCanarySnapshot, now time.Time) api.QueueBindingCanary {
	out := r.Report
	out.DeliveredAt = nil
	out.CompletedAt = nil
	switch {
	case r.Cancelled:
		out.Status, out.Reason = "cancelled", "cancelled"
	case !out.ExpiresAt.After(now):
		out.Status, out.Reason = "expired", "deadline_expired"
	case current == nil || !sameQueueCanarySnapshot(r.Snapshot, *current):
		out.Status, out.Reason = "blocked", "consumer_changed"
	case inv.ReplayGeneration != 0 || inv.Attempts > 1:
		out.Status, out.Reason = "failed", "attempt_changed"
	case inv.State == InvocationCompleted:
		if inv.CompletedAt != nil && inv.ReceivedAt != nil && !inv.ReceivedAt.Before(out.PublishedAt) && !inv.CompletedAt.Before(*inv.ReceivedAt) && !inv.CompletedAt.After(now) && inv.CompletedAt.Before(out.ExpiresAt) && r.CompletedDeploymentID == out.DeploymentID && r.CompletedAttempt == 1 && r.CompletedGeneration == 0 {
			out.Status = "succeeded"
			out.DeliveredAt = inv.ReceivedAt
			out.CompletedAt = inv.CompletedAt
		} else {
			out.Status, out.Reason = "failed", "completion_unconfirmed"
		}
	case inv.State == InvocationPending && inv.Attempts > 0:
		out.Status, out.Reason = "failed", "dispatch_failed"
	case inv.State == InvocationFailed || inv.State == InvocationDeadLetter || inv.State == InvocationCancelled:
		out.Status, out.Reason = "failed", "handler_failed"
	case inv.Attempts > 0:
		out.Status = "dispatching"
	default:
		out.Status = "published"
	}
	return out
}

func QueueCanaryDeploymentSupports(dep Deployment, binding string) bool {
	return queueCanarySupported(queueCanarySnapshot{Name: binding, OverrideEnv: dep.OverrideEnv, RootfsKey: dep.RootfsKey, ImageDigest: dep.ImageDigest})
}

// Preserve the producer's plan queue-depth limit and safe error metadata.
type QueueCanaryCapacityError struct{ Limit, Observed int }

func (e *QueueCanaryCapacityError) Error() string { return ErrQuotaExceeded.Error() }
func (e *QueueCanaryCapacityError) Unwrap() error { return ErrQuotaExceeded }
func queueCanaryProducerCapacity(plan api.Plan, depth int64) error {
	if err := environmentQueueProducerCapacity(plan, depth); err != nil {
		if errors.Is(err, ErrQuotaExceeded) {
			limits, _ := api.LimitsFor(plan)
			return &QueueCanaryCapacityError{Limit: limits.MaxQueueDepth, Observed: int(depth)}
		}
		return err
	}
	return nil
}
