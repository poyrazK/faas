package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func syncPlaybookOutcomeEscalationTx(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, id pgtype.UUID, scope string, w api.OperationWorkflowPlaybookEvidenceWatch) error {
	if w.Escalation == nil {
		return nil
	}
	payload, err := q.LockReadinessNotification(ctx, tx, sqlc.LockReadinessNotificationParams{SubscriptionID: id, Sequence: w.Escalation.Sequence})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return mapErr(err)
	}
	var n api.OperationWorkflowReadinessNotification
	if err = json.Unmarshal(payload, &n); err != nil {
		return err
	}
	updated, ok := syncPlaybookOutcomeEscalation(n, w)
	if !ok {
		return nil
	}
	updated = captureObligationReceipts(ctx, uuid.UUID(id.Bytes).String(), scope, n, updated, "")
	payload, err = json.Marshal(updated)
	if err != nil {
		return err
	}
	_, err = q.UpdateReadinessNotification(ctx, tx, sqlc.UpdateReadinessNotificationParams{SubscriptionID: id, Sequence: w.Escalation.Sequence, Payload: payload})
	return mapErr(err)
}

func syncPlaybookStepAssignmentsTx(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, id pgtype.UUID, scope string, w api.OperationWorkflowPlaybookEvidenceWatch, p api.OperationWorkflowPlaybookProgress) error {
	for _, ref := range append(slices.Clone(w.StepAssignments), w.ReviewAssignments...) {
		raw, err := q.LockReadinessNotification(ctx, tx, sqlc.LockReadinessNotificationParams{SubscriptionID: id, Sequence: ref.Notification.Sequence})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return mapErr(err)
		}
		var n api.OperationWorkflowReadinessNotification
		if err = json.Unmarshal(raw, &n); err != nil {
			return err
		}
		updated, ok := syncPlaybookStepAssignment(n, p, ref)
		if !ok {
			continue
		}
		updated = captureObligationReceipts(ctx, uuid.UUID(id.Bytes).String(), scope, n, updated, "")
		raw, err = json.Marshal(updated)
		if err != nil {
			return err
		}
		if _, err = q.UpdateReadinessNotification(ctx, tx, sqlc.UpdateReadinessNotificationParams{SubscriptionID: id, Sequence: ref.Notification.Sequence, Payload: raw}); err != nil {
			return mapErr(err)
		}
	}
	return nil
}
