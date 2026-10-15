package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var errAutomaticPolicyAuthority = errors.New("invalid automatic policy authority")
var errAutomaticPollingData = errors.New("invalid automatic polling data")

func automaticPollingFailureCode(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ""
	}
	if errors.Is(err, errAutomaticPolicyAuthority) {
		return "invalid_policy_authority"
	}
	if errors.Is(err, errAutomaticPollingData) {
		return "invalid_polling_data"
	}
	switch manualObservationFailureCode(err) {
	case "invalid_recovery_identity":
		return "invalid_recovery_identity"
	case "invalid_observation_data":
		return "invalid_polling_data"
	}
	return ""
}

func nextAutomaticPollingFailure(previous *api.WorkflowRecoveryAutomaticPollingFailure, code string, now time.Time) *api.WorkflowRecoveryAutomaticPollingFailure {
	attempts := int64(1)
	if previous != nil && previous.Attempts > 0 {
		attempts = previous.Attempts
		if attempts < api.WorkflowAutomaticPollingMaxAttempts {
			attempts++
		}
		if attempts > api.WorkflowAutomaticPollingMaxAttempts {
			attempts = api.WorkflowAutomaticPollingMaxAttempts
		}
	}
	delay := api.WorkflowRecoveryPolicyPollInterval
	for i := int64(1); i < attempts && delay < api.WorkflowAutomaticPollingMaxBackoff; i++ {
		delay *= 2
	}
	if delay > api.WorkflowAutomaticPollingMaxBackoff {
		delay = api.WorkflowAutomaticPollingMaxBackoff
	}
	now = now.UTC().Truncate(time.Microsecond)
	return &api.WorkflowRecoveryAutomaticPollingFailure{Code: code, Attempts: attempts, FailedAt: now, RetryAt: now.Add(delay)}
}

func (m *MemStore) deferAutomaticPollingMem(p *memWorkflowRecoveryPolicy, err error, now time.Time) (bool, error) {
	code := automaticPollingFailureCode(err)
	if code == "" {
		return false, err
	}
	failure := nextAutomaticPollingFailure(p.Response.AutomaticExecution.PollingFailure, code, now)
	app, _ := m.eventSubscriptionAppLocked(p.Response.AppID)
	payload := pollingAlertTransition(app, "automatic_policy", "", p.Response.Version, code, failure.Attempts, &failure.RetryAt, p.Response.PollingNotification, now)
	n, captureErr := m.capturePollingAlertLocked(app, payload)
	if captureErr != nil {
		return false, captureErr
	}
	if n != nil {
		p.Response.PollingNotification = n
	}
	p.Response.AutomaticExecution.PollingFailure = failure
	p.NextExecute = failure.RetryAt
	return true, nil
}

func validateAutomaticPollingMemLocked(m *MemStore, p *memWorkflowRecoveryPolicy) error {
	if err := (api.SetWorkflowRecoveryPolicyRequest{Policy: p.Response.Policy}).Validate(); err != nil {
		return fmt.Errorf("%w: %w", errAutomaticPolicyAuthority, err)
	}
	for _, raw := range p.Reviews {
		var r api.WorkflowRecoveryPolicyReview
		if err := json.Unmarshal(raw, &r); err != nil {
			return fmt.Errorf("decode automatic polling review: %w", err)
		}
	}
	for _, r := range m.initialRecoveryReviews {
		if !sameMemUUID(r.AppID, p.Response.AppID) || r.Policy.Mode != "automatic_initial" || (r.Status != "prepared" && !initialReviewUnresolved(r)) {
			continue
		}
		for _, id := range []string{r.ID, r.AppID, r.Target.RunID} {
			v, err := uuid.Parse(id)
			if err != nil || v == uuid.Nil {
				return errManualObservationIdentity
			}
		}
		if r.Status == "submitted" {
			if err := validateManualObservationIdentity(r); err != nil {
				return err
			}
		}
	}
	if e, r := recoveryAutomaticPendingMem(p); e != nil {
		if _, err := recoveryAutomaticAction(r, *e, p.Response); err != nil {
			return err
		}
	}
	return nil
}

// Snapshot only this app's execution-owned state while the caller holds m.mu.
// Read-only outstanding inspection happens before this boundary and is fenced
// again under the mutex, so rollback never overwrites concurrent operator work.
type automaticPollingMemSnapshot struct {
	Policy  memWorkflowRecoveryPolicy
	Reviews map[string]api.WorkflowInitialRecoveryReview
	Jobs    map[string]*memEventRecoveryJob
	Outbox  map[string]appWebhookOutboxEvent
}

func (m *MemStore) runAutomaticPollingMemLocked(ctx context.Context, p *memWorkflowRecoveryPolicy, app App, now time.Time, work func() error) (bool, error) {
	before := automaticPollingMemSnapshot{Policy: *p, Reviews: map[string]api.WorkflowInitialRecoveryReview{}, Jobs: map[string]*memEventRecoveryJob{}, Outbox: map[string]appWebhookOutboxEvent{}}
	for id, r := range m.initialRecoveryReviews {
		if sameMemUUID(r.AppID, app.ID) {
			before.Reviews[id] = r
		}
	}
	for id, j := range m.eventRecoveryJobs {
		if sameMemUUID(j.Job.AppID, app.ID) {
			before.Jobs[id] = j
		}
	}
	for id, e := range m.appWebhookEventOutbox {
		if sameMemUUID(e.AppID, app.ID) {
			before.Outbox[id] = e
		}
	}
	raw, err := json.Marshal(before)
	if err != nil {
		return m.deferAutomaticPollingMem(p, fmt.Errorf("%w: encode rollback state: %w", errAutomaticPollingData, err), now)
	}
	var saved automaticPollingMemSnapshot
	if err = json.Unmarshal(raw, &saved); err != nil {
		return false, fmt.Errorf("decode automatic rollback state: %w", err)
	}
	before = saved
	p.NextExecute = now.Add(api.WorkflowRecoveryPolicyPollInterval)
	p.Response.AutomaticExecution.PollingFailure = nil
	for id, at := range p.Charges {
		if !at.After(now.Add(-time.Duration(api.WorkflowRecoveryPolicyMaxCooldownSeconds) * time.Second)) {
			delete(p.Charges, id)
		}
	}
	err = work()
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		n, captureErr := m.capturePollingAlertLocked(app, pollingAlertTransition(app, "automatic_policy", "", p.Response.Version, "", 0, nil, p.Response.PollingNotification, now))
		err = captureErr
		if err == nil {
			if n != nil {
				p.Response.PollingNotification = n
			}
			return true, nil
		}
	}
	*p = before.Policy
	for id, r := range m.initialRecoveryReviews {
		if sameMemUUID(r.AppID, app.ID) {
			delete(m.initialRecoveryReviews, id)
		}
	}
	for id, r := range before.Reviews {
		m.initialRecoveryReviews[id] = r
	}
	for id, j := range m.eventRecoveryJobs {
		if sameMemUUID(j.Job.AppID, app.ID) {
			delete(m.eventRecoveryJobs, id)
		}
	}
	for id, j := range before.Jobs {
		m.eventRecoveryJobs[id] = j
	}
	for id, e := range m.appWebhookEventOutbox {
		if sameMemUUID(e.AppID, app.ID) {
			delete(m.appWebhookEventOutbox, id)
		}
	}
	for id, e := range before.Outbox {
		m.appWebhookEventOutbox[id] = e
	}
	return m.deferAutomaticPollingMem(p, err, now)
}

func validateAutomaticInitialIdentity(r api.WorkflowInitialRecoveryReview) error {
	for _, id := range []string{r.ID, r.AppID, r.Target.RunID} {
		v, err := uuid.Parse(id)
		if err != nil || v == uuid.Nil {
			return errManualObservationIdentity
		}
	}
	if r.Status == "submitted" {
		return validateManualObservationIdentity(r)
	}
	return nil
}
