package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func syncPlaybookOutcomeEscalationTx(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, id pgtype.UUID, w api.OperationWorkflowPlaybookEvidenceWatch) error {
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
	payload, err = json.Marshal(updated)
	if err != nil {
		return err
	}
	_, err = q.UpdateReadinessNotification(ctx, tx, sqlc.UpdateReadinessNotificationParams{SubscriptionID: id, Sequence: w.Escalation.Sequence, Payload: payload})
	return mapErr(err)
}
