package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrLineageControlCommandConflict = errors.New("state: lineage control command UUID reused with different inputs")
var ErrLineageControlReceiptLimit = errors.New("state: lineage control receipt limit reached")

func lineageControlCommandID(id string) (string, error) {
	if id == "" {
		return "", nil
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		return "", ErrInvalidArgument
	}
	return parsed.String(), nil
}
func newLineageControlReceipt(job api.EventExecutionLineageRetryJob, action, command string, sequence int64, at time.Time, actor lineageControlActor) api.EventExecutionLineageRetryJobControlReceipt {
	return api.EventExecutionLineageRetryJobControlReceipt{ActorKind: actor.Kind, ActorID: actor.ID, CredentialType: actor.CredentialType, CommandID: command, Sequence: sequence, JobRequestID: job.RequestID, SubscriptionID: job.SubscriptionID, AppID: job.AppID, Action: action, ExpectedCreatedAt: job.CreatedAt, ResultStatus: job.Status, Processed: job.Processed, Selected: len(job.Items), JobUpdatedAt: job.UpdatedAt, RecordedAt: at, RetainUntil: job.RetainUntil}
}
func lineageControlReceiptFromRow(row sqlc.EventExecutionLineageRetryControlReceipt) (api.EventExecutionLineageRetryJobControlReceipt, error) {
	var receipt api.EventExecutionLineageRetryJobControlReceipt
	err := json.Unmarshal(row.Result, &receipt)
	receipt.CommandID, receipt.JobRequestID = uuidString(row.CommandID), uuidString(row.JobRequestID)
	receipt.SubscriptionID, receipt.AppID = uuidString(row.SubscriptionID), uuidString(row.AppID)
	receipt.Action, receipt.Sequence = row.Action, row.Sequence
	receipt.ExpectedCreatedAt = timeFromPgtype(row.ExpectedCreatedAt)
	receipt.RecordedAt, receipt.RetainUntil = timeFromPgtype(row.RecordedAt), timeFromPgtype(row.RetainUntil)
	return receipt, err
}
func replayLineageControlReceipt(job *api.EventExecutionLineageRetryJob, receipt api.EventExecutionLineageRetryJobControlReceipt, action string, expected time.Time) error {
	if receipt.Action != action || !receipt.ExpectedCreatedAt.Equal(expected) || !receipt.RetainUntil.Equal(job.RetainUntil) {
		return ErrLineageControlCommandConflict
	}
	job.ControlReceipt = &receipt
	return nil
}
func lineageControlHistoryLimit(after int64, limit int) (int, error) {
	if after < 0 || after > api.EventExecutionLineageRetryControlMaxReceipts {
		return 0, ErrInvalidArgument
	}
	if limit == 0 {
		limit = api.EventExecutionLineageRetryControlPageDefault
	}
	if limit < 1 || limit > api.EventExecutionLineageRetryControlPageMax {
		return 0, ErrInvalidArgument
	}
	return limit, nil
}
func lineageControlHistoryResult(controls []api.EventExecutionLineageRetryJobControlReceipt, limit int) api.EventExecutionLineageRetryJobControls {
	out := api.EventExecutionLineageRetryJobControls{Controls: controls}
	if len(controls) > limit {
		out.Controls = controls[:limit]
		out.NextAfter = out.Controls[limit-1].Sequence
	}
	return out
}

func (s *PgStore) ListEventExecutionLineageRetryJobControls(ctx context.Context, account, app, sub, id string, expected time.Time, after int64, limit int, filters ...api.EventExecutionLineageRetryJobControlFilters) (api.EventExecutionLineageRetryJobControls, error) {
	out := api.EventExecutionLineageRetryJobControls{Controls: []api.EventExecutionLineageRetryJobControlReceipt{}}
	if err := validLineageJobCancellation(account, app, sub, id, api.CancelEventExecutionLineageRetryJobRequest{ExpectedCreatedAt: expected}); err != nil {
		return out, err
	}
	filter, err := lineageControlHistoryFilters(filters)
	if err != nil {
		return out, err
	}
	limit, err = lineageControlHistoryLimit(after, limit)
	if err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	_, err = q.EventConsumerExecutionTarget(ctx, tx, sqlc.EventConsumerExecutionTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	job, err := q.GetEventExecutionLineageRetryJob(ctx, tx, sqlc.GetEventExecutionLineageRetryJobParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), RequestID: mustPgUUID(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if !timeFromPgtype(job.CreatedAt).Equal(expected) {
		return out, ErrConflict
	}
	rows, err := q.ListEventExecutionLineageRetryControlReceipts(ctx, tx, sqlc.ListEventExecutionLineageRetryControlReceiptsParams{AccountID: job.AccountID, AppID: job.AppID, SubscriptionID: job.SubscriptionID, JobRequestID: job.RequestID, ExpectedCreatedAt: job.CreatedAt, AfterSequence: after, PageLimit: int32(limit + 1), ActionFilter: filter.Action, ActorIDFilter: filter.ActorID, CredentialTypeFilter: filter.CredentialType})
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		receipt, err := lineageControlReceiptFromRow(row)
		if err != nil {
			return out, err
		}
		out.Controls = append(out.Controls, receipt)
	}
	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	return lineageControlHistoryResult(out.Controls, limit), nil
}
func (m *MemStore) ListEventExecutionLineageRetryJobControls(ctx context.Context, account, app, sub, id string, expected time.Time, after int64, limit int, filters ...api.EventExecutionLineageRetryJobControlFilters) (api.EventExecutionLineageRetryJobControls, error) {
	out := api.EventExecutionLineageRetryJobControls{Controls: []api.EventExecutionLineageRetryJobControlReceipt{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validLineageJobCancellation(account, app, sub, id, api.CancelEventExecutionLineageRetryJobRequest{ExpectedCreatedAt: expected}); err != nil {
		return out, err
	}
	filter, err := lineageControlHistoryFilters(filters)
	if err != nil {
		return out, err
	}
	limit, err = lineageControlHistoryLimit(after, limit)
	if err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	coverage := m.eventExecutionCoverage[canonicalMemUUID(sub)]
	job, ok := coverage.jobs[canonicalMemUUID(id)]
	if !ok || !job.RetainUntil.After(time.Now()) {
		return out, ErrNotFound
	}
	if !job.CreatedAt.Equal(expected) {
		return out, ErrConflict
	}
	for _, receipt := range coverage.controlReceipts[canonicalMemUUID(id)] {
		if receipt.Sequence > after && matchesLineageControlHistory(receipt, filter) {
			out.Controls = append(out.Controls, receipt)
		}
	}
	sort.Slice(out.Controls, func(i, j int) bool { return out.Controls[i].Sequence < out.Controls[j].Sequence })
	return lineageControlHistoryResult(out.Controls, limit), nil
}

func lineageControlHistoryFilters(filters []api.EventExecutionLineageRetryJobControlFilters) (api.EventExecutionLineageRetryJobControlFilters, error) {
	var out api.EventExecutionLineageRetryJobControlFilters
	if len(filters) > 1 {
		return out, ErrInvalidArgument
	}
	if len(filters) == 1 {
		out = filters[0]
	}
	normalized, err := out.Normalize()
	if err != nil {
		return out, ErrInvalidArgument
	}
	return normalized, nil
}
func matchesLineageControlHistory(receipt api.EventExecutionLineageRetryJobControlReceipt, filter api.EventExecutionLineageRetryJobControlFilters) bool {
	return (filter.Action == "" || receipt.Action == filter.Action) &&
		(filter.ActorID == "" || receipt.ActorID == filter.ActorID) &&
		(filter.CredentialType == "" || receipt.CredentialType == filter.CredentialType)
}
