package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func recoveryRequestUUID(id string) pgtype.UUID {
	if id == "" {
		return pgtype.UUID{}
	}
	return mustPgUUID(id)
}

func recoveryItemLineage(out *api.EventRecoveryItem, encoded []byte) {
	var identity executionRecoveryIdentity
	if json.Unmarshal(encoded, &identity) != nil || identity.ParentJobID == "" || identity.ParentPosition <= 0 {
		return
	}
	out.ParentJobID = identity.ParentJobID
	position := identity.ParentPosition
	out.ParentPosition = &position
}

func validateRecoveryRetryParent(parent sqlc.EventRecoveryJob, app string) error {
	if uuidString(parent.AppID) != canonicalMemUUID(app) {
		return ErrNotFound
	}
	var selection api.EventRecoveryRequest
	if err := json.Unmarshal(parent.Selection, &selection); err != nil {
		return err
	}
	if selection.Mode != "execution" || parent.State != "completed" && parent.State != "cancelled" {
		return ErrEventRecoveryRetryParent
	}
	return nil
}

// Called after the account range lock and before quota checks or new selection.
// Lock the retained child before reading so pruning cannot turn an uncertain
// creation retry into a second selection. Parent pruning does not break reuse.
func prepareRecoveryRetry(ctx context.Context, q *sqlc.Queries, tx sqlc.DBTX, account, app string, req api.EventRecoveryRequest) (*api.EventRecoveryJob, error) {
	if req.ParentJobID == "" {
		return nil, nil
	}
	id, err := q.EventRecoveryRetryExisting(ctx, tx, sqlc.EventRecoveryRetryExistingParams{AccountID: mustPgUUID(account), RequestID: mustPgUUID(req.RequestID)})
	if err == nil {
		job, readErr := getEventRecovery(ctx, q, tx, account, uuidString(id))
		if readErr != nil {
			return nil, readErr
		}
		if job.AppID != canonicalMemUUID(app) || job.Selection != req {
			return nil, ErrEventRecoveryRequestConflict
		}
		return &job, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	parent, err := q.EventRecoveryLock(ctx, tx, sqlc.EventRecoveryLockParams{AccountID: mustPgUUID(account), JobID: mustPgUUID(req.ParentJobID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return nil, validateRecoveryRetryParent(parent, app)
}

func eventRecoveryRetryCandidates(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, app string, req api.EventRecoveryRequest, now time.Time) ([]sqlc.EventRecoveryCandidatesRow, error) {
	parent, err := q.EventRecoveryPreflightJob(ctx, db, sqlc.EventRecoveryPreflightJobParams{AccountID: mustPgUUID(account), JobID: mustPgUUID(req.ParentJobID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = validateRecoveryRetryParent(parent, app); err != nil {
		return nil, err
	}
	rows, err := q.EventRecoveryRetryCandidates(ctx, db, sqlc.EventRecoveryRetryCandidatesParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), ParentJobID: mustPgUUID(req.ParentJobID), SubscriptionID: req.SubscriptionID, EventSource: req.EventSource, EventType: req.EventType, Outcome: req.Outcome, NowAt: pgtypeFromTime(now), FailedBefore: pgtypeFromTime(now.Add(-time.Duration(req.MinAgeSeconds) * time.Second)), PageLimit: api.EventRecoveryRecipientsMax + 1})
	if err != nil {
		return nil, err
	}
	out := make([]sqlc.EventRecoveryCandidatesRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, sqlc.EventRecoveryCandidatesRow(row))
	}
	return out, nil
}

func (m *MemStore) eventRecoveryRetryParentLocked(account, app, id string) (*memEventRecoveryJob, error) {
	parent, err := m.eventRecoveryJobLocked(account, id)
	if err != nil {
		return nil, err
	}
	if !sameMemUUID(parent.Job.AppID, app) {
		return nil, ErrNotFound
	}
	if parent.Job.Selection.Mode != "execution" || parent.Job.State != "completed" && parent.Job.State != "cancelled" {
		return nil, ErrEventRecoveryRetryParent
	}
	return parent, nil
}

func (m *MemStore) eventRecoveryRetryCandidatesLocked(ctx context.Context, account, app string, req api.EventRecoveryRequest, now time.Time) ([]memEventRecoveryItem, error) {
	if !m.eventRecoveryAppLocked(account, app) {
		return nil, ErrNotFound
	}
	parent, err := m.eventRecoveryRetryParentLocked(account, app, req.ParentJobID)
	if err != nil {
		return nil, err
	}
	items := []memEventRecoveryItem{}
	for _, item := range parent.Items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if item.State != "queued" || item.ReplayGeneration == nil {
			continue
		}
		saved, ok := m.eventRecoveryExecutionResults[recoveryResultKey{parent.Job.ID, item.Position}]
		if !ok || saved.InvocationID != item.ReplayInvocationID || saved.Generation != *item.ReplayGeneration || !saved.CreatedAt.Equal(item.ReplayCreatedAt) || saved.Execution.RecordedAt == nil || saved.Execution.RecordedAt.After(now) {
			continue
		}
		state := InvocationState(saved.Execution.State)
		if state == "dead_lettered" {
			state = InvocationDeadLetter
		}
		if state != InvocationFailed && state != InvocationDeadLetter || req.Outcome != "" && req.Outcome != string(state) {
			continue
		}
		if req.SubscriptionID != "" && req.SubscriptionID != item.SubscriptionID || req.EventSource != "" && req.EventSource != item.EventSource || req.EventType != "" && req.EventType != item.EventType {
			continue
		}
		failedAt := *saved.Execution.RecordedAt
		if saved.Execution.CompletedAt != nil {
			failedAt = *saved.Execution.CompletedAt
		}
		if failedAt.After(now.Add(-time.Duration(req.MinAgeSeconds) * time.Second)) {
			continue
		}
		identity := executionRecoveryIdentity{ParentJobID: parent.Job.ID, ParentPosition: item.Position, InvocationID: saved.InvocationID, State: state, Attempts: saved.Execution.Attempts, Generation: saved.Generation, CreatedAt: saved.CreatedAt, CompletedAt: cloneEventReceiptTime(saved.Execution.CompletedAt)}
		if inv, retained := m.invocations[saved.InvocationID]; retained && sameMemUUID(inv.AccountID, account) && sameMemUUID(inv.AppID, app) && inv.CreatedAt.Equal(saved.CreatedAt) && inv.ReplayGeneration == saved.Generation {
			for _, dead := range m.deadLetterEventsLocked(app) {
				if dead.Source == "invocation" && dead.SourceID == saved.InvocationID && dead.ReplayedAt == nil {
					identity.DeadLetterID = dead.ID
					break
				}
			}
		}
		encoded, err := json.Marshal(identity)
		if err != nil {
			return nil, err
		}
		position := item.Position
		items = append(items, memEventRecoveryItem{OutboxID: item.OutboxID, ExpectedProgress: encoded, EventRecoveryItem: api.EventRecoveryItem{ParentJobID: parent.Job.ID, ParentPosition: &position, InvocationID: saved.InvocationID, Position: int64(len(items) + 1), EventSource: item.EventSource, EventID: item.EventID, EventType: item.EventType, SubscriptionID: item.SubscriptionID, FailedAt: failedAt, FailureCode: string(state), Retryable: true, State: "pending"}})
	}
	return items, nil
}
