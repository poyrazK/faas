package state

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// SelectedTraceStore is an optional, metadata-only read capability. It never
// falls back to account-wide reads. Empty selections must fail closed.
type SelectedTraceStore interface {
	ListSelectedTraceRequests(context.Context, sqlc.ListSelectedTraceRequestsParams) ([]sqlc.ListSelectedTraceRequestsRow, error)
	ListSelectedTraceInvocations(context.Context, sqlc.ListSelectedTraceInvocationsParams) ([]sqlc.ListSelectedTraceInvocationsRow, error)
}

func validSelectedTraceRead(account pgtype.UUID, ids []pgtype.UUID, traceID string, limit int32) bool {
	if !api.ValidTraceID(traceID) || !account.Valid || account.Bytes == [16]byte{} || len(ids) == 0 || len(ids) > api.SelectedTraceAppsMax || limit < 1 || limit > api.SelectedTraceRowsMax+1 {
		return false
	}
	for _, id := range ids {
		if !id.Valid || id.Bytes == [16]byte{} {
			return false
		}
	}
	return true
}

func (s *PgStore) ListSelectedTraceRequests(ctx context.Context, arg sqlc.ListSelectedTraceRequestsParams) ([]sqlc.ListSelectedTraceRequestsRow, error) {
	if !validSelectedTraceRead(arg.AccountID, arg.AppIds, arg.TraceID, arg.RowLimit) {
		return nil, ErrInvalidArgument
	}
	return (&sqlc.Queries{}).ListSelectedTraceRequests(ctx, s.pool, arg)
}

func (s *PgStore) ListSelectedTraceInvocations(ctx context.Context, arg sqlc.ListSelectedTraceInvocationsParams) ([]sqlc.ListSelectedTraceInvocationsRow, error) {
	if !validSelectedTraceRead(arg.AccountID, arg.AppIds, arg.TraceID, arg.RowLimit) {
		return nil, ErrInvalidArgument
	}
	return (&sqlc.Queries{}).ListSelectedTraceInvocations(ctx, s.pool, arg)
}

// MemStore does not persist debugger request telemetry; report unavailable.
func (m *MemStore) ListSelectedTraceRequests(ctx context.Context, arg sqlc.ListSelectedTraceRequestsParams) ([]sqlc.ListSelectedTraceRequestsRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validSelectedTraceRead(arg.AccountID, arg.AppIds, arg.TraceID, arg.RowLimit) {
		return nil, ErrInvalidArgument
	}
	return nil, errMemStoreRequestTelemetry
}

func (m *MemStore) ListSelectedTraceInvocations(ctx context.Context, arg sqlc.ListSelectedTraceInvocationsParams) ([]sqlc.ListSelectedTraceInvocationsRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validSelectedTraceRead(arg.AccountID, arg.AppIds, arg.TraceID, arg.RowLimit) {
		return nil, ErrInvalidArgument
	}
	selected := map[string]bool{}
	for _, id := range arg.AppIds {
		selected[uuid.UUID(id.Bytes).String()] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []sqlc.ListSelectedTraceInvocationsRow{}
	for _, inv := range m.invocations {
		appUUID, parseErr := uuid.Parse(inv.AppID)
		if parseErr != nil || !sameTraceUUID(inv.AccountID, uuid.UUID(arg.AccountID.Bytes).String()) || !selected[appUUID.String()] {
			continue
		}
		var headers map[string]string
		if json.Unmarshal(inv.Headers, &headers) != nil || headers["X-Gregale-Trace-Id"] != arg.TraceID {
			continue
		}
		invID, idErr := uuid.Parse(inv.ID)
		if idErr != nil || inv.Attempts < 0 || int64(inv.Attempts) > int64(1<<31-1) {
			return nil, ErrInvalidArgument
		}
		row := sqlc.ListSelectedTraceInvocationsRow{AppID: pgtype.UUID{Bytes: appUUID, Valid: true}, ID: pgtype.UUID{Bytes: invID, Valid: true}, Source: string(inv.Source), State: string(inv.State), Attempts: int32(inv.Attempts), CreatedAt: pgtype.Timestamptz{Time: inv.CreatedAt, Valid: true}}
		if inv.ReceivedAt != nil {
			row.ReceivedAt = pgtype.Timestamptz{Time: *inv.ReceivedAt, Valid: true}
		}
		if inv.CompletedAt != nil {
			row.CompletedAt = pgtype.Timestamptz{Time: *inv.CompletedAt, Valid: true}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Time.Equal(rows[j].CreatedAt.Time) {
			return uuid.UUID(rows[i].ID.Bytes).String() < uuid.UUID(rows[j].ID.Bytes).String()
		}
		return rows[i].CreatedAt.Time.Before(rows[j].CreatedAt.Time)
	})
	return rows[:min(len(rows), int(arg.RowLimit))], nil
}

var _ SelectedTraceStore = (*PgStore)(nil)
var _ SelectedTraceStore = (*MemStore)(nil)

func sameTraceUUID(a, b string) bool {
	x, e1 := uuid.Parse(a)
	y, e2 := uuid.Parse(b)
	return e1 == nil && e2 == nil && x == y
}
