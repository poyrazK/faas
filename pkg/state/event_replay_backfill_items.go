package state

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const eventReplayBackfillItemsCursorPrefix = "erbi1."

type eventReplayBackfillItemsCursor struct {
	Version    int       `json:"v"`
	AccountID  string    `json:"account_id"`
	JobID      string    `json:"job_id"`
	State      string    `json:"state"`
	AcceptedAt time.Time `json:"accepted_at"`
	OutboxID   int64     `json:"outbox_id"`
}

func normalizeEventReplayBackfillItemsQuery(accountID, jobID string, query *api.EventReplayBackfillItemsQuery) (string, string, eventReplayBackfillItemsCursor, error) {
	account, accountErr := uuid.Parse(accountID)
	job, jobErr := uuid.Parse(jobID)
	if accountErr != nil || jobErr != nil {
		return "", "", eventReplayBackfillItemsCursor{}, ErrEventReplayBackfillQuery
	}
	accountID, jobID = account.String(), job.String()
	if query.Limit == 0 {
		query.Limit = api.EventReplayBackfillItemsPageDefault
	}
	if query.Limit < 1 || query.Limit > api.EventReplayBackfillItemsPageMax {
		return "", "", eventReplayBackfillItemsCursor{}, ErrEventReplayBackfillQuery
	}
	switch query.State {
	case "", "pending", "processing", "enqueued", "filtered", "failed", "skipped_captured", "skipped_unknown", "skipped_existing", "skipped_unsettled":
	default:
		return "", "", eventReplayBackfillItemsCursor{}, ErrEventReplayBackfillQuery
	}
	cursor := eventReplayBackfillItemsCursor{Version: 1, AccountID: accountID, JobID: jobID, State: query.State}
	if query.After == "" {
		return accountID, jobID, cursor, nil
	}
	invalid := func() (string, string, eventReplayBackfillItemsCursor, error) {
		return "", "", eventReplayBackfillItemsCursor{}, ErrEventReplayBackfillQuery
	}
	if len(query.After) > api.EventReplayBackfillItemsCursorMaxBytes || !strings.HasPrefix(query.After, eventReplayBackfillItemsCursorPrefix) {
		return invalid()
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(query.After, eventReplayBackfillItemsCursorPrefix))
	if err != nil {
		return invalid()
	}
	var decoded eventReplayBackfillItemsCursor
	if json.Unmarshal(data, &decoded) != nil || decoded.Version != 1 || decoded.AccountID != accountID || decoded.JobID != jobID || decoded.State != query.State || decoded.OutboxID <= 0 || decoded.AcceptedAt.IsZero() {
		return invalid()
	}
	decoded.AcceptedAt = decoded.AcceptedAt.UTC()
	return accountID, jobID, decoded, nil
}

// ListEventReplayBackfillItems returns a bounded, stable page of event-level
// outcomes. Event identity is snapshotted on the item, so the page remains
// useful after retention removes the original envelope.
func (s *PgStore) ListEventReplayBackfillItems(ctx context.Context, accountID, jobID string, query api.EventReplayBackfillItemsQuery) (api.EventReplayBackfillItemsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventReplayBackfillRequestTimeout)
	defer cancel()
	accountID, jobID, cursor, err := normalizeEventReplayBackfillItemsQuery(accountID, jobID, &query)
	if err != nil {
		return api.EventReplayBackfillItemsResponse{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.EventReplayBackfillItemsResponse{}, fmt.Errorf("begin event replay backfill item read: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.EventReplayBackfillRequestTimeout)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := sqlc.New()
	exists, err := q.EventReplayBackfillExists(ctx, tx, sqlc.EventReplayBackfillExistsParams{ID: mustPgUUID(jobID), AccountID: mustPgUUID(accountID)})
	if err != nil {
		return api.EventReplayBackfillItemsResponse{}, fmt.Errorf("check event replay backfill item scope: %w", err)
	}
	if !exists {
		return api.EventReplayBackfillItemsResponse{}, ErrNotFound
	}
	rows, err := q.EventReplayBackfillItems(ctx, tx, sqlc.EventReplayBackfillItemsParams{
		JobID: mustPgUUID(jobID), State: query.State,
		AfterAt: pgtype.Timestamptz{Time: cursor.AcceptedAt.UTC(), Valid: true}, AfterOutboxID: cursor.OutboxID,
		PageLimit: int32(query.Limit + 1),
	})
	if err != nil {
		return api.EventReplayBackfillItemsResponse{}, fmt.Errorf("read event replay backfill items: %w", err)
	}
	hasMore := len(rows) > query.Limit
	if hasMore {
		rows = rows[:query.Limit]
	}
	out := api.EventReplayBackfillItemsResponse{JobID: jobID, Items: make([]api.EventReplayBackfillItem, 0, len(rows))}
	for _, row := range rows {
		failureCode := eventHistoryText(row.FailureCode, api.EventRoutingHistoryCodeMaxBytes)
		lastError := eventHistoryText(row.LastError, api.EventRoutingHistoryErrorMaxBytes)
		item := api.EventReplayBackfillItem{
			EventSource: row.EventSource, EventID: row.EventID, EventType: row.EventType, SchemaVersion: row.SchemaVersion,
			AcceptedAt: timeFromPgtype(row.AcceptedAt), State: row.State, Attempts: int(row.Attempts),
			FailureCode: failureCode, LastError: lastError, DetailsTruncated: failureCode != row.FailureCode || lastError != row.LastError,
			Retryable: row.Retryable, UpdatedAt: timeFromPgtype(row.UpdatedAt),
		}
		if row.ConsumerKind == "workflow" {
			item.WorkflowRunID = row.WorkflowRunID
			item.WorkflowRunStatus = row.WorkflowRunStatus
		}
		if row.ReceiptAvailable {
			item.ReceiptURL = "/v1/events/receipt?" + url.Values{"source": {row.EventSource}, "id": {row.EventID}}.Encode()
		}
		if row.ConsumerKind == "application" && row.ExecutionHistoryAvailable {
			item.AttemptHistoryURL = "/v1/events/receipt/attempts?" + url.Values{"source": {row.EventSource}, "id": {row.EventID}, "subscription_id": {uuidString(row.SubscriptionID)}}.Encode()
		}
		out.Items = append(out.Items, item)
	}
	if hasMore {
		last := rows[len(rows)-1]
		data, err := json.Marshal(eventReplayBackfillItemsCursor{
			Version: 1, AccountID: accountID, JobID: jobID, State: query.State,
			AcceptedAt: timeFromPgtype(last.AcceptedAt), OutboxID: last.OutboxID,
		})
		if err != nil {
			return api.EventReplayBackfillItemsResponse{}, fmt.Errorf("encode event replay backfill item cursor: %w", err)
		}
		out.NextAfter = eventReplayBackfillItemsCursorPrefix + base64.RawURLEncoding.EncodeToString(data)
	}
	if err := tx.Commit(ctx); err != nil {
		return api.EventReplayBackfillItemsResponse{}, fmt.Errorf("commit event replay backfill item read: %w", err)
	}
	return out, nil
}
