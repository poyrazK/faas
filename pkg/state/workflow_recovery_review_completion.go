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
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type WorkflowRecoveryReviewCompletionNotificationStore interface {
	ProcessNextWorkflowRecoveryReviewCompletion(context.Context, time.Time) (bool, error)
	WorkflowRecoveryReviewCompletionNotificationAllowed(context.Context, AppWebhookDelivery) (bool, string, error)
}

func recoveryReviewCompletionID(id string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:recovery-review:"+id+":finished")).String()
}
func recoveryReviewCompletionPayload(r api.WorkflowRecoveryPolicyReview, n api.WorkflowRecoveryReviewCompletionNotification) api.WorkflowRecoveryReviewFinishedWebhookPayload {
	path := "/v1/apps/" + url.PathEscape(r.Plan.App) + "/recovery-policy/reviews/" + r.ID
	return api.WorkflowRecoveryReviewFinishedWebhookPayload{EventID: n.EventID, ReviewID: r.ID, AppID: r.AppID, App: r.Plan.App, JobID: r.JobID, PolicyVersion: r.PolicyVersion, CapturedAt: n.CapturedAt, Result: n.Result, ReviewURL: path, StatusURL: path + "/status", StatusCommand: "gregale automations recovery-policy-reviews --app " + strconv.Quote(r.Plan.App) + " --review " + r.ID + " --status"}
}
func newRecoveryReviewCompletion(id string, result api.WorkflowRecoveryReviewStatus, receivers []string, now time.Time) *api.WorkflowRecoveryReviewCompletionNotification {
	status := "queued"
	if len(receivers) == 0 {
		status = "no_receivers"
	}
	return &api.WorkflowRecoveryReviewCompletionNotification{EventID: recoveryReviewCompletionID(id), Status: status, CapturedAt: now, ReceiverWebhookIDs: receivers, Result: result}
}
func (s *PgStore) ProcessNextWorkflowRecoveryReviewCompletion(ctx context.Context, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrEventRecoveryQuery
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin review completion capture: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.WorkflowRecoveryReviewCompletionClaim(ctx, tx, pgtypeFromTime(now))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim review completion: %w", err)
	}
	id, account, app := uuidString(row.ReviewID), uuidString(row.AccountID), uuidString(row.AppID)
	result, checkErr := InspectWorkflowRecoveryReview(ctx, s, account, app, id, now)
	if checkErr != nil || !result.Complete {
		if err = q.WorkflowRecoveryReviewCompletionDefer(ctx, tx, sqlc.WorkflowRecoveryReviewCompletionDeferParams{ReviewID: row.ReviewID, NextAt: pgtypeFromTime(now.Add(api.WorkflowRecoveryPolicyPollInterval))}); err != nil {
			return false, fmt.Errorf("defer review completion: %w", err)
		}
		return true, tx.Commit(ctx)
	}
	review, err := s.GetWorkflowRecoveryPolicyReview(ctx, account, app, id)
	if err != nil {
		return false, fmt.Errorf("read completed review: %w", err)
	}
	receivers, err := q.WorkflowRecoveryReviewNotificationReceivers(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationReceiversParams{AccountID: row.AccountID, AppID: row.AppID, Event: string(AppWebhookEventRecoveryReviewFinished)})
	if err != nil {
		return false, fmt.Errorf("select review completion receivers: %w", err)
	}
	ids := []string{}
	for _, id := range receivers {
		ids = append(ids, uuidString(id))
	}
	receipt := newRecoveryReviewCompletion(id, result, ids, now)
	raw, err := json.Marshal(receipt)
	if err != nil {
		return false, err
	}
	if err = q.WorkflowRecoveryReviewCompletionCapture(ctx, tx, sqlc.WorkflowRecoveryReviewCompletionCaptureParams{ReviewID: row.ReviewID, Completion: raw}); err != nil {
		return false, fmt.Errorf("capture review completion: %w", err)
	}
	if receipt.Status == "queued" {
		payload, err := json.Marshal(recoveryReviewCompletionPayload(review, *receipt))
		if err != nil {
			return false, err
		}
		if err = q.WorkflowRecoveryReviewNotificationEnqueue(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationEnqueueParams{EventID: mustPgUUID(receipt.EventID), AccountID: row.AccountID, AppID: row.AppID, Event: string(AppWebhookEventRecoveryReviewFinished), ReviewID: row.ReviewID, Payload: payload, ReceiverIds: receivers, NowAt: pgtypeFromTime(now)}); err != nil {
			return false, fmt.Errorf("enqueue review completion: %w", err)
		}
	}
	return true, tx.Commit(ctx)
}
func (m *MemStore) ProcessNextWorkflowRecoveryReviewCompletion(ctx context.Context, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrEventRecoveryQuery
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	m.mu.Lock()
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
				return false, fmt.Errorf("decode completion review: %w", err)
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
				n = &memRecoveryReviewNotification{Next: r.ObservedAt, NextCompletion: r.ObservedAt}
				p.Notifications[id] = n
			}
			if n.Completion == nil && !n.NextCompletion.After(now) && (chosen == nil || n.NextCompletion.Before(chosen.NextCompletion) || n.NextCompletion.Equal(chosen.NextCompletion) && id < review.ID) {
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
	result, checkErr := InspectWorkflowRecoveryReview(ctx, m, account, review.AppID, review.ID, now)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if chosen.Completion != nil {
		return false, nil
	}
	p := m.recoveryPolicies[review.AppID]
	if p == nil || p.Reviews[review.ID] == nil || p.Notifications[review.ID] != chosen || m.eventRecoveryJobs[review.JobID] == nil {
		return false, nil
	}
	if checkErr != nil || !result.Complete {
		chosen.NextCompletion = now.Add(api.WorkflowRecoveryPolicyPollInterval)
		return true, nil
	}
	receivers := []string{}
	for _, h := range m.appWebhooks {
		if h.Scope == AppWebhookScopeApp && sameMemUUID(h.AccountID, account) && sameMemUUID(h.AppID, review.AppID) && h.Enabled && appWebhookMatches(h.EventFilter, AppWebhookEventRecoveryReviewFinished) {
			receivers = append(receivers, h.ID)
		}
	}
	sort.Strings(receivers)
	receipt := newRecoveryReviewCompletion(review.ID, result, receivers, now)
	if receipt.Status == "queued" {
		payload, err := json.Marshal(recoveryReviewCompletionPayload(review, *receipt))
		if err != nil {
			return false, err
		}
		if m.appWebhookEventOutbox == nil {
			m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
		}
		m.appWebhookEventOutbox[receipt.EventID] = appWebhookOutboxEvent{ID: receipt.EventID, AccountID: account, AppID: review.AppID, Event: AppWebhookEventRecoveryReviewFinished, SourceID: review.ID, Payload: payload, RecipientWebhookIDs: append([]string{}, receivers...), CreatedAt: now}
	}
	chosen.Completion = recoveryPolicyClone(receipt)
	return true, nil
}
func recoveryReviewCompletionAllowed(ctx context.Context, s WorkflowRecoveryReviewInspector, row AppWebhookDelivery) (bool, string, error) {
	if row.Event != AppWebhookEventRecoveryReviewFinished {
		return false, "invalid_event", nil
	}
	var payload api.WorkflowRecoveryReviewFinishedWebhookPayload
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		return false, "invalid_payload", nil //nolint:nilerr // Invalid retained payloads are terminal delivery rejections, not retriable store failures.
	}
	if _, err := uuid.Parse(payload.ReviewID); err != nil || payload.EventID != recoveryReviewCompletionID(payload.ReviewID) || !sameMemUUID(payload.AppID, row.AppID) {
		return false, "invalid_scope", nil //nolint:nilerr // Invalid retained payloads are terminal delivery rejections, not retriable store failures.
	}
	r, err := s.GetWorkflowRecoveryPolicyReview(ctx, row.AccountID, row.AppID, payload.ReviewID)
	if errors.Is(err, ErrNotFound) {
		return false, "review_not_retained", nil
	}
	if err != nil {
		return false, "", err
	}
	n := r.CompletionNotification
	if r.Plan == nil || n == nil || n.Status != "queued" || !n.Result.Complete || n.EventID != payload.EventID || !recoveryPolicyHas(n.ReceiverWebhookIDs, row.WebhookID) {
		return false, "capture_mismatch", nil
	}
	if !reflect.DeepEqual(recoveryReviewCompletionPayload(r, *n), payload) {
		return false, "payload_mismatch", nil
	}
	return true, "complete", nil
}
func (s *PgStore) WorkflowRecoveryReviewCompletionNotificationAllowed(ctx context.Context, row AppWebhookDelivery) (bool, string, error) {
	return recoveryReviewCompletionAllowed(ctx, s, row)
}
func (m *MemStore) WorkflowRecoveryReviewCompletionNotificationAllowed(ctx context.Context, row AppWebhookDelivery) (bool, string, error) {
	return recoveryReviewCompletionAllowed(ctx, m, row)
}
