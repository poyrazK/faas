package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type WorkflowRecoveryReviewNotificationStore interface {
	ProcessNextWorkflowRecoveryReviewNotification(context.Context, time.Time) (bool, error)
	WorkflowRecoveryReviewNotificationAllowed(context.Context, AppWebhookDelivery, time.Time) (bool, string, error)
}
type memRecoveryReviewNotification struct {
	NextCompletion time.Time
	Completion     *api.WorkflowRecoveryReviewCompletionNotification
	Next           time.Time
	Receipt        *api.WorkflowRecoveryReviewNotification
}

func recoveryReviewNotificationID(id string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:recovery-review:"+id+":ready")).String()
}
func recoveryReviewReadyPayload(r api.WorkflowRecoveryPolicyReview, now time.Time) api.WorkflowRecoveryReviewReadyWebhookPayload {
	p := r.Plan
	path := "/v1/apps/" + url.PathEscape(p.App) + "/recovery-policy/reviews/" + r.ID
	command := "gregale automations recovery-policy-reviews --app " + strconv.Quote(p.App) + " --review " + r.ID
	out := api.WorkflowRecoveryReviewReadyWebhookPayload{EventID: recoveryReviewNotificationID(r.ID), ReviewID: r.ID, AppID: r.AppID, App: p.App, JobID: r.JobID, PolicyVersion: r.PolicyVersion, PreparedAt: p.CreatedAt, ObservedAt: now, ReviewURL: path, StatusURL: path + "/status", ReviewCommand: command, ExportCommand: command + " --plan-out recovery-plan.json", Targets: []api.WorkflowRecoveryPolicyDecision{}}
	for _, d := range r.Decisions {
		if d.Outcome == "prepared" {
			out.Targets = append(out.Targets, d)
		}
	}
	return out
}

// Unknown freshness defers capture and delivery; terminal dispositions suppress it.
func recoveryReviewNeedsNotification(ctx context.Context, s WorkflowRecoveryReviewInspector, account, app, id string, now time.Time) (api.WorkflowRecoveryPolicyReview, bool, string, error) {
	r, err := s.GetWorkflowRecoveryPolicyReview(ctx, account, app, id)
	if errors.Is(err, ErrNotFound) {
		return r, false, "review_not_retained", nil
	}
	if err != nil {
		return r, false, "", err
	}
	if r.Policy.Mode == "automatic_retry" {
		return r, false, "automatic_policy", nil
	}
	if r.ReviewDecision != nil {
		return r, false, r.ReviewDecision.Decision, nil
	}
	status, err := InspectWorkflowRecoveryReview(ctx, s, account, app, id, now)
	if err != nil {
		return r, false, "", err
	}
	if status.Status == "unknown" {
		return r, false, "", fmt.Errorf("review freshness remains unknown")
	}
	return r, status.Status == "ready", status.Status, nil
}
func newRecoveryReviewNotification(id, reason string, allowed bool, receivers []string, now time.Time) *api.WorkflowRecoveryReviewNotification {
	status := "suppressed"
	if allowed {
		status = "queued"
		if len(receivers) == 0 {
			status = "no_receivers"
		}
	}
	return &api.WorkflowRecoveryReviewNotification{EventID: recoveryReviewNotificationID(id), Status: status, Reason: reason, CapturedAt: now, ReceiverWebhookIDs: receivers}
}
func (m *MemStore) decorateRecoveryReviewNotificationLocked(p *memWorkflowRecoveryPolicy, r *api.WorkflowRecoveryPolicyReview) {
	if r.Plan != nil {
		actions := []api.WorkflowRecoveryAutomaticAction{}
		for _, a := range r.Plan.Actions {
			if e := p.Executions[api.WorkflowRecoveryResponseActionID(*r.Plan, a)]; e != nil {
				actions = append(actions, recoveryPolicyClone(*e))
			}
		}
		if len(actions) > 0 {
			r.AutomaticExecution = &api.WorkflowRecoveryReviewAutomaticExecution{Actions: actions}
		}
	}

	if n := p.Notifications[r.ID]; n != nil {
		if n.Receipt != nil {
			r.Notification = recoveryPolicyClone(n.Receipt)
		}
		if n.Completion != nil {
			r.CompletionNotification = recoveryPolicyClone(n.Completion)
		}
	}
}
func (s *PgStore) ProcessNextWorkflowRecoveryReviewNotification(ctx context.Context, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrEventRecoveryQuery
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.WorkflowRecoveryReviewNotificationClaim(ctx, tx, pgtypeFromTime(now))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim recovery review notification: %w", err)
	}
	id, account, app := uuidString(row.ReviewID), uuidString(row.AccountID), uuidString(row.AppID)
	review, allowed, reason, checkErr := recoveryReviewNeedsNotification(ctx, s, account, app, id, now)
	if checkErr != nil {
		if err = q.WorkflowRecoveryReviewNotificationDefer(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationDeferParams{ReviewID: row.ReviewID, NextAt: pgtypeFromTime(now.Add(api.WorkflowRecoveryPolicyPollInterval))}); err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	receivers := []string{}
	receiverIDs := []pgtype.UUID{}
	if allowed {
		receiverIDs, err = q.WorkflowRecoveryReviewNotificationReceivers(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationReceiversParams{AccountID: row.AccountID, AppID: row.AppID, Event: string(AppWebhookEventRecoveryReviewReady)})
		if err != nil {
			return false, err
		}
		for _, id := range receiverIDs {
			receivers = append(receivers, uuidString(id))
		}
	}
	receipt := newRecoveryReviewNotification(id, reason, allowed, receivers, now)
	raw, err := json.Marshal(receipt)
	if err != nil {
		return false, err
	}
	if err = q.WorkflowRecoveryReviewNotificationCapture(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationCaptureParams{ReviewID: row.ReviewID, Receipt: raw}); err != nil {
		return false, err
	}
	if receipt.Status == "queued" {
		payload, err := json.Marshal(recoveryReviewReadyPayload(review, now))
		if err != nil {
			return false, err
		}
		if err = q.WorkflowRecoveryReviewNotificationEnqueue(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationEnqueueParams{EventID: mustPgUUID(receipt.EventID), Event: string(AppWebhookEventRecoveryReviewReady), AccountID: row.AccountID, AppID: row.AppID, ReviewID: row.ReviewID, Payload: payload, ReceiverIds: receiverIDs, NowAt: pgtypeFromTime(now)}); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}
func (m *MemStore) ProcessNextWorkflowRecoveryReviewNotification(ctx context.Context, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrEventRecoveryQuery
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	m.mu.Lock()
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return false, err
	}
	var chosen *memRecoveryReviewNotification
	var review api.WorkflowRecoveryPolicyReview
	var account string
	for app, p := range m.recoveryPolicies {
		if p.Notifications == nil {
			p.Notifications = map[string]*memRecoveryReviewNotification{}
		}
		for id, raw := range p.Reviews {
			var r api.WorkflowRecoveryPolicyReview
			if err := json.Unmarshal(raw, &r); err != nil {
				m.mu.Unlock()
				return false, err
			}
			job := m.eventRecoveryJobs[r.JobID]
			if job == nil {
				forgetRecoveryPolicyReview(p, id)
				continue
			}
			if r.Plan == nil {
				continue
			}
			n := p.Notifications[id]
			if n == nil {
				n = &memRecoveryReviewNotification{Next: r.ObservedAt}
				p.Notifications[id] = n
			}
			if n.Receipt == nil && !n.Next.After(now) && (chosen == nil || n.Next.Before(chosen.Next) || n.Next.Equal(chosen.Next) && id < review.ID) {
				chosen = n
				review = r
				review.AppID = app
				account = job.AccountID
			}
		}
	}
	m.mu.Unlock()
	if chosen == nil {
		return false, nil
	}
	current, allowed, reason, checkErr := recoveryReviewNeedsNotification(ctx, m, account, review.AppID, review.ID, now)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if chosen.Receipt != nil {
		return false, nil
	}
	if checkErr != nil {
		chosen.Next = now.Add(api.WorkflowRecoveryPolicyPollInterval)
		return true, nil
	}
	// Recheck disposition under the write lock before capture.
	p := m.recoveryPolicies[review.AppID]
	var retained api.WorkflowRecoveryPolicyReview
	if p == nil || json.Unmarshal(p.Reviews[review.ID], &retained) != nil || m.eventRecoveryJobs[review.JobID] == nil {
		allowed = false
		reason = "review_not_retained"
	} else if retained.ReviewDecision != nil {
		allowed = false
		reason = retained.ReviewDecision.Decision
	}
	receivers := []string{}
	if allowed {
		for _, h := range m.appWebhooks {
			if h.Scope == AppWebhookScopeApp && sameMemUUID(h.AccountID, account) && sameMemUUID(h.AppID, review.AppID) && h.Enabled && appWebhookMatches(h.EventFilter, AppWebhookEventRecoveryReviewReady) {
				receivers = append(receivers, h.ID)
			}
		}
		sort.Strings(receivers)
	}
	receipt := newRecoveryReviewNotification(review.ID, reason, allowed, receivers, now)
	if receipt.Status == "queued" {
		payload, err := json.Marshal(recoveryReviewReadyPayload(current, now))
		if err != nil {
			return false, err
		}
		if m.appWebhookEventOutbox == nil {
			m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
		}
		m.appWebhookEventOutbox[receipt.EventID] = appWebhookOutboxEvent{ID: receipt.EventID, AccountID: account, AppID: review.AppID, Event: AppWebhookEventRecoveryReviewReady, SourceID: review.ID, Payload: payload, RecipientWebhookIDs: append([]string{}, receivers...), CreatedAt: now}
	}
	chosen.Receipt = receipt
	return true, nil
}
func recoveryReviewNotificationAllowed(ctx context.Context, s WorkflowRecoveryReviewInspector, row AppWebhookDelivery, now time.Time) (bool, string, error) {
	if row.Event != AppWebhookEventRecoveryReviewReady {
		return false, "invalid_event", nil
	}
	var payload api.WorkflowRecoveryReviewReadyWebhookPayload
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		return false, "invalid_payload", nil //nolint:nilerr // Invalid retained payloads are terminal delivery rejections, not retriable store failures.
	}
	if _, err := uuid.Parse(payload.ReviewID); err != nil || payload.EventID != recoveryReviewNotificationID(payload.ReviewID) || !sameMemUUID(payload.AppID, row.AppID) {
		return false, "invalid_scope", nil //nolint:nilerr // Invalid retained payloads are terminal delivery rejections, not retriable store failures.
	}
	if payload.InitialFailureID != "" {
		return initialReviewNotificationAllowed(ctx, s, row, payload, now)
	}
	r, allowed, reason, err := recoveryReviewNeedsNotification(ctx, s, row.AccountID, row.AppID, payload.ReviewID, now)
	if err != nil || !allowed {
		return allowed, reason, err
	}
	if r.Notification == nil || r.Notification.Status != "queued" || r.Notification.EventID != payload.EventID || r.JobID != payload.JobID || !recoveryPolicyHas(r.Notification.ReceiverWebhookIDs, row.WebhookID) {
		return false, "capture_mismatch", nil
	}
	if !reflect.DeepEqual(recoveryReviewReadyPayload(r, r.Notification.CapturedAt), payload) {
		return false, "payload_mismatch", nil
	}
	return true, "ready", nil
}
func (s *PgStore) WorkflowRecoveryReviewNotificationAllowed(ctx context.Context, row AppWebhookDelivery, now time.Time) (bool, string, error) {
	return recoveryReviewNotificationAllowed(ctx, s, row, now)
}
func (m *MemStore) WorkflowRecoveryReviewNotificationAllowed(ctx context.Context, row AppWebhookDelivery, now time.Time) (bool, string, error) {
	return recoveryReviewNotificationAllowed(ctx, m, row, now)
}
