package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func workflowRecoveryCandidates(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, app string, req api.EventRecoveryRequest) ([]sqlc.EventRecoveryCandidatesRow, error) {
	p := req.WorkflowRouting
	rows := make([]sqlc.EventRecoveryCandidatesRow, 0, len(p.Events))
	for _, target := range p.Events {
		row, err := q.EventWorkflowRecoveryCandidate(ctx, db, sqlc.EventWorkflowRecoveryCandidateParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), EventSource: p.Source, EventID: target.EventID, SubscriptionID: target.SubscriptionID, WorkflowName: p.Name})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEventRecoveryChanged
		}
		if err != nil {
			return nil, fmt.Errorf("select workflow recovery recipient: %w", err)
		}
		rows = append(rows, sqlc.EventRecoveryCandidatesRow(row))
	}
	return rows, nil
}

func (m *MemStore) workflowRecoveryCandidatesLocked(ctx context.Context, account, app string, req api.EventRecoveryRequest) ([]memEventRecoveryItem, error) {
	if !m.eventRecoveryAppLocked(account, app) {
		return nil, ErrNotFound
	}
	p := req.WorkflowRouting
	items := make([]memEventRecoveryItem, 0, len(p.Events))
	for _, target := range p.Events {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		found := false
		for _, work := range m.eventFanout {
			var event publishedEventIdentity
			if json.Unmarshal(work.Payload, &event) != nil || event.Source != p.Source || event.ID != target.EventID {
				continue
			}
			for _, recipient := range work.RecipientSnapshot {
				if !sameMemUUID(recipient.AccountID, account) || !sameMemUUID(recipient.AppID, app) || recipient.ID != target.SubscriptionID || !eventRecoveryWorkflowMatches(recipient, p.Name) {
					continue
				}
				progress := work.RecipientProgress[recipient.ID]
				if progress.State != PublishedEventRecipientFailed {
					continue
				}
				encoded, err := memEventRecoveryIdentity(progress)
				if err != nil {
					return nil, err
				}
				failedAt := progress.UpdatedAt
				if failedAt.IsZero() {
					failedAt = work.CreatedAt
				}
				code := progress.FailureCode
				if code == "" {
					code = EventFanoutFailureCodeUnknown
				}
				items = append(items, memEventRecoveryItem{OutboxID: work.ID, ExpectedProgress: encoded, EventRecoveryItem: api.EventRecoveryItem{Position: int64(len(items) + 1), EventSource: event.Source, EventID: event.ID, EventType: event.Type, SubscriptionID: recipient.ID, FailedAt: failedAt, FailureCode: eventHistoryText(code, api.EventRoutingHistoryCodeMaxBytes), Retryable: progress.Retryable, State: "pending"}})
				found = true
			}
		}
		if !found {
			return nil, ErrEventRecoveryChanged
		}
	}
	return items, nil
}

func eventWorkflowRecoveryExpectation(p *api.EventWorkflowRecoveryPlan, source, event, sub string) (api.EventRoutingRecoveryExpectation, bool) {
	for _, t := range p.Events {
		if p.Source == source && t.EventID == event && t.SubscriptionID == sub {
			return p.Expectation(t), true
		}
	}
	return api.EventRoutingRecoveryExpectation{}, false
}
func workflowRecoveryOutcome(err error) (string, string, error) {
	switch {
	case err == nil:
		return "queued", "", nil
	case errors.Is(err, ErrEventRecoveryChanged), errors.Is(err, ErrConflict):
		return "skipped", "changed", nil
	case errors.Is(err, ErrNotFound):
		return "skipped", "receipt_expired", nil
	case errors.Is(err, ErrEventDeliveryExpired):
		return "skipped", "expired", nil
	default:
		return "", "", err
	}
}
func processWorkflowRecoveryTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, job sqlc.EventRecoveryJob, item sqlc.EventRecoveryItem, selection api.EventRecoveryRequest, now time.Time) (string, string, error) {
	expectation, ok := eventWorkflowRecoveryExpectation(selection.WorkflowRouting, item.EventSource, item.EventID, item.SubscriptionID)
	if !ok {
		return "skipped", "changed", nil
	}
	err := replayWorkflowRecoveryTx(ctx, q, tx, uuidString(job.AccountID), uuidString(job.AppID), item.EventSource, item.EventID, item.SubscriptionID, expectation, now)
	return workflowRecoveryOutcome(err)
}
func (m *MemStore) processWorkflowRecoveryLocked(job *memEventRecoveryJob, item *memEventRecoveryItem, now time.Time) (string, string, error) {
	p := job.Job.Selection.WorkflowRouting
	expectation, ok := eventWorkflowRecoveryExpectation(p, item.EventSource, item.EventID, item.SubscriptionID)
	if !ok {
		return "skipped", "changed", nil
	}
	if !m.eventRecoveryAppLocked(job.AccountID, job.Job.AppID) {
		return "skipped", "target_unavailable", nil
	}
	published := false
	owned, _ := m.eventSubscriptionAppLocked(job.Job.AppID)
	for _, a := range m.automationRecordsLocked(owned.ID) {
		if a.Name == p.Name && a.PublishedVersion == p.PublishedVersion {
			published = true
		}
	}
	if !published {
		return "skipped", "changed", nil
	}
	return workflowRecoveryOutcome(m.replayFailedEventRecipientLocked(job.AccountID, job.Job.AppID, item.EventSource, item.EventID, item.SubscriptionID, false, &expectation, now))
}

func cloneWorkflowRecoverySelection(req api.EventRecoveryRequest) api.EventRecoveryRequest {
	if req.WorkflowRuns != nil {
		p := *req.WorkflowRuns
		p.Runs = append([]api.WorkflowRunRecoveryTarget(nil), p.Runs...)
		req.WorkflowRuns = &p
	}
	if req.WorkflowRouting == nil {
		return req
	}
	p := *req.WorkflowRouting
	p.Events = append([]api.EventWorkflowRecoveryTarget(nil), p.Events...)
	for i := range p.Events {
		if p.Events[i].Generation != nil {
			v := *p.Events[i].Generation
			p.Events[i].Generation = &v
		}
	}
	req.WorkflowRouting = &p
	return req
}
