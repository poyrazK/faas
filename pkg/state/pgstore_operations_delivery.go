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

func operationDeliveryPGRow(r sqlc.GetCustomerOperationDeliveryRowRow) AppWebhookDelivery {
	d := AppWebhookDelivery{ID: r.ID, WebhookID: r.WebhookID, AppID: r.AppID, AccountID: r.AccountID, Event: AppWebhookEvent(r.Event), Payload: r.Payload, Attempt: int(r.Attempt), Status: AppWebhookDeliveryStatus(r.Status), LastError: r.LastError.String, LastResponseCode: int(r.LastResponseCode.Int32), NextAttemptAt: r.NextAttemptAt.Time}
	if r.DeliveredAt.Valid {
		v := r.DeliveredAt.Time
		d.DeliveredAt = &v
	}
	return d
}

func (s *PgStore) OperationDeliverySnapshot(ctx context.Context, account, id string) (api.OperationDeliveryInspection, error) {
	op, err := s.OperationByID(ctx, account, "", id)
	if err != nil {
		return api.OperationDeliveryInspection{}, err
	}
	now := time.Now().UTC()
	if op.CompletionDelivery.DeliveryID == "" {
		return operationDeliveryObservation(op, nil, 0, now), nil
	}
	def, err := s.OperationDefinitionByID(ctx, account, op.DefinitionID)
	if err != nil {
		return api.OperationDeliveryInspection{}, err
	}
	a, _ := operationUUID(account)
	d, _ := operationUUID(op.CompletionDelivery.DeliveryID)
	row, err := sqlc.New().GetCustomerOperationDeliveryRow(ctx, s.pool, sqlc.GetCustomerOperationDeliveryRowParams{ID: d, AccountID: a})
	if errors.Is(err, pgx.ErrNoRows) {
		return operationDeliveryObservation(op, nil, 0, now), nil
	}
	if err != nil {
		return api.OperationDeliveryInspection{}, err
	}
	delivery := operationDeliveryPGRow(row)
	if !operationDeliveryOwned(op, def, delivery) {
		return api.OperationDeliveryInspection{}, ErrNotFound
	}
	return operationDeliveryObservation(op, &delivery, int(row.ReplayGeneration), now), nil
}

func (s *PgStore) RetryOperationCompletionDelivery(ctx context.Context, account, id string, req api.OperationDeliveryRetryRequest) (api.OperationDeliveryRetryResponse, error) {
	empty := api.OperationDeliveryRetryResponse{}
	if err := validateOperationDeliveryRetry(req); err != nil {
		return empty, err
	}
	a, err := operationUUID(account)
	if err != nil {
		return empty, ErrNotFound
	}
	i, err := operationUUID(id)
	if err != nil {
		return empty, ErrNotFound
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return empty, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	raw, err := q.LockCustomerOperationDeliveryOwner(ctx, tx, sqlc.LockCustomerOperationDeliveryOwnerParams{ID: i, AccountID: a})
	if err != nil {
		return empty, mapErr(err)
	}
	var op Operation
	if err := json.Unmarshal(raw, &op); err != nil {
		return empty, err
	}
	now := time.Now().UTC()
	if !operationRetained(op, now) || !op.ExpiresAt.Truncate(time.Microsecond).After(now.Truncate(time.Microsecond)) {
		return empty, ErrOperationExpired
	}
	prior, err := q.GetCustomerOperationDeliveryRetry(ctx, tx, sqlc.GetCustomerOperationDeliveryRetryParams{OperationID: i, RetryID: req.RetryID})
	if err == nil {
		r := api.OperationDeliveryRetryResponse{OperationID: op.ID, RetryID: req.RetryID, DeliveryID: prior.DeliveryID, ExpectedReplayGeneration: int(prior.ExpectedReplayGeneration), ReplayGeneration: int(prior.ReplayGeneration), State: "queued", QueuedAt: prior.QueuedAt.Time.UTC(), ExpiresAt: prior.ExpiresAt.Time.UTC()}
		if !operationDeliveryReceiptMatches(r, req) {
			return empty, ErrOperationInputConflict
		}
		return r, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return empty, err
	}
	if !operationDeliveryIDsEqual(req.DeliveryID, op.CompletionDelivery.DeliveryID) {
		return empty, ErrConflict
	}
	plan, err := q.GetCustomerOperationDeliveryPlan(ctx, tx, a)
	if err != nil {
		return empty, mapErr(err)
	}
	limits, ok := api.LimitsFor(api.Plan(plan))
	if !ok || limits.WebhookPerApp == 0 {
		return empty, NewOperationLimitError("completion_delivery_plan", 0, 1)
	}
	count, err := q.CountCustomerOperationDeliveryRetries(ctx, tx, i)
	if err != nil {
		return empty, err
	}
	if count >= api.OperationDeliveryRetriesMax {
		return empty, NewOperationLimitError("completion_delivery_retries", api.OperationDeliveryRetriesMax, count+1)
	}
	defID, _ := operationUUID(op.DefinitionID)
	defRow, err := q.GetCustomerOperationDefinition(ctx, tx, sqlc.GetCustomerOperationDefinitionParams{ID: defID, AccountID: a})
	if err != nil {
		return empty, mapErr(err)
	}
	def, err := operationPGDefinition(defRow)
	if err != nil {
		return empty, err
	}
	d, _ := operationUUID(op.CompletionDelivery.DeliveryID)
	row, err := q.LockCustomerOperationDeliveryRow(ctx, tx, sqlc.LockCustomerOperationDeliveryRowParams{ID: d, AccountID: a})
	if err != nil {
		return empty, mapErr(err)
	}
	if !operationDeliveryOwned(op, def, operationDeliveryPGRow(sqlc.GetCustomerOperationDeliveryRowRow(row))) {
		return empty, ErrNotFound
	}
	if row.Status != "dead" || int(row.ReplayGeneration) != *req.ExpectedReplayGeneration {
		return empty, ErrConflict
	}
	r := newOperationDeliveryReceipt(op, req, now)
	ts := pgtype.Timestamptz{Time: r.QueuedAt, Valid: true}
	changed, err := q.ResetCustomerOperationDelivery(ctx, tx, sqlc.ResetCustomerOperationDeliveryParams{ID: d, AccountID: a, ExpectedReplayGeneration: int32(*req.ExpectedReplayGeneration), Now: ts})
	if err != nil {
		return empty, err
	}
	if changed != 1 {
		return empty, ErrConflict
	}
	err = q.InsertCustomerOperationDeliveryRetry(ctx, tx, sqlc.InsertCustomerOperationDeliveryRetryParams{OperationID: i, RetryID: req.RetryID, DeliveryID: d, ExpectedReplayGeneration: int32(r.ExpectedReplayGeneration), ReplayGeneration: int32(r.ReplayGeneration), QueuedAt: ts, ExpiresAt: pgtype.Timestamptz{Time: r.ExpiresAt, Valid: true}})
	if err != nil {
		return empty, err
	}
	if err := tx.Commit(ctx); err != nil {
		return empty, err
	}
	return r, nil
}
