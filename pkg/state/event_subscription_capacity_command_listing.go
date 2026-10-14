package state

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventSubscriptionCapacityCommandListStore interface {
	ListEventSubscriptionCapacityCommands(context.Context, string, string, string, api.EventSubscriptionCapacityCommandsQuery) (api.EventSubscriptionCapacityCommands, error)
}

var _ EventSubscriptionCapacityCommandListStore = (*PgStore)(nil)
var _ EventSubscriptionCapacityCommandListStore = (*MemStore)(nil)

type capacityCommandCursor struct {
	Version      int       `json:"version"`
	Account      string    `json:"account"`
	App          string    `json:"app"`
	Subscription string    `json:"subscription"`
	Outcome      string    `json:"outcome"`
	RecordedAt   time.Time `json:"recorded_at"`
	Command      string    `json:"command"`
}

func encodeCapacityCommandCursor(c capacityCommandCursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func capacityCommandPage(account, app, sub string, query api.EventSubscriptionCapacityCommandsQuery) (api.EventSubscriptionCapacityCommandsQuery, capacityCommandCursor, error) {
	c := capacityCommandCursor{Version: 1, Account: canonicalMemUUID(account), App: canonicalMemUUID(app), Subscription: canonicalMemUUID(sub), Outcome: query.Outcome, Command: uuid.Nil.String()}
	if query.Outcome == "" {
		query.Outcome = "any"
		c.Outcome = "any"
	}
	if query.Limit == 0 {
		query.Limit = api.EventSubscriptionCapacityCommandPageDefault
	}
	if validSubscriptionControlIDs(account, app, sub) != nil || query.Limit < 1 || query.Limit > api.EventSubscriptionCapacityCommandPageMax || len(query.After) > api.EventSubscriptionCapacityCommandCursorMaxBytes || (query.Outcome != "any" && query.Outcome != "applied" && query.Outcome != "unchanged") {
		return query, c, ErrInvalidArgument
	}
	if query.After == "" {
		return query, c, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(query.After)
	var saved capacityCommandCursor
	if err != nil || json.Unmarshal(raw, &saved) != nil || encodeCapacityCommandCursor(saved) != query.After || saved.Version != 1 || saved.Account != c.Account || saved.App != c.App || saved.Subscription != c.Subscription || saved.Outcome != query.Outcome || saved.RecordedAt.IsZero() || saved.RecordedAt.Year() < 1 || saved.RecordedAt.Location() != time.UTC || !saved.RecordedAt.Equal(saved.RecordedAt.Truncate(time.Microsecond)) || validateCapacityCommandRead(account, app, sub, saved.Command) != nil || saved.Command != canonicalMemUUID(saved.Command) {
		return query, c, ErrInvalidArgument
	}
	return query, saved, nil
}
func capacityCommandHistoryResult(account, app, sub string, query api.EventSubscriptionCapacityCommandsQuery, now time.Time, receipts []api.EventSubscriptionCapacityCommandReceipt) api.EventSubscriptionCapacityCommands {
	out := api.EventSubscriptionCapacityCommands{AppID: canonicalMemUUID(app), SubscriptionID: canonicalMemUUID(sub), Outcome: query.Outcome, ObservedAt: now, Receipts: receipts}
	if len(receipts) > query.Limit {
		out.HasMore = true
		out.Receipts = receipts[:query.Limit]
		last := out.Receipts[len(out.Receipts)-1]
		out.NextAfter = encodeCapacityCommandCursor(capacityCommandCursor{Version: 1, Account: canonicalMemUUID(account), App: out.AppID, Subscription: out.SubscriptionID, Outcome: query.Outcome, RecordedAt: last.RecordedAt.UTC(), Command: last.CommandID})
	}
	return out
}
func (s *PgStore) ListEventSubscriptionCapacityCommands(ctx context.Context, account, app, sub string, query api.EventSubscriptionCapacityCommandsQuery) (api.EventSubscriptionCapacityCommands, error) {
	var out api.EventSubscriptionCapacityCommands
	query, cursor, err := capacityCommandPage(account, app, sub, query)
	if err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.EventConsumerPressureTarget(ctx, tx, sqlc.EventConsumerPressureTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)}); err != nil {
		return out, mapErr(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	rows, err := q.ListEventSubscriptionCapacityCommands(ctx, tx, sqlc.ListEventSubscriptionCapacityCommandsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), NowAt: pgtypeFromTime(now), Outcome: query.Outcome, HasAfter: query.After != "", AfterRecordedAt: pgtypeFromTime(cursor.RecordedAt), AfterCommandID: mustPgUUID(cursor.Command), PageLimit: int32(query.Limit + 1)})
	if err != nil {
		return out, err
	}
	receipts := make([]api.EventSubscriptionCapacityCommandReceipt, 0, len(rows))
	for _, row := range rows {
		receipt, readErr := decodeCapacityCommandRead(row.Request, row.Receipt, app, sub, uuidString(row.CommandID), now)
		if readErr != nil {
			return out, readErr
		}
		if !receipt.RecordedAt.Equal(timeFromPgtype(row.RecordedAt)) || !receipt.RetainUntil.Equal(timeFromPgtype(row.RetainUntil)) {
			return out, fmt.Errorf("capacity command retention metadata changed")
		}
		receipts = append(receipts, receipt)
	}
	out = capacityCommandHistoryResult(account, app, sub, query, now, receipts)
	return out, tx.Commit(ctx)
}
func (m *MemStore) ListEventSubscriptionCapacityCommands(ctx context.Context, account, app, sub string, query api.EventSubscriptionCapacityCommandsQuery) (api.EventSubscriptionCapacityCommands, error) {
	var out api.EventSubscriptionCapacityCommands
	query, cursor, err := capacityCommandPage(account, app, sub, query)
	if err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	receipts := []api.EventSubscriptionCapacityCommandReceipt{}
	for key, row := range m.eventSubscriptionCapacityCommands {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		if !sameMemUUID(row.AccountID, account) || !sameMemUUID(row.AppID, app) || !sameMemUUID(row.SubscriptionID, sub) || !row.RetainUntil.After(now) {
			continue
		}
		prefix, command, keyOK := strings.Cut(key, "/")
		if !keyOK || prefix != canonicalMemUUID(sub) || validateCapacityCommandRead(account, app, sub, command) != nil || command != canonicalMemUUID(command) {
			return out, fmt.Errorf("invalid capacity command identity")
		}
		var saved api.EventSubscriptionCapacityCommandReceipt
		if err = json.Unmarshal(row.Receipt, &saved); err != nil {
			return out, err
		}
		if saved.RecordedAt.After(now) || (query.Outcome != "any" && saved.Outcome != query.Outcome) || (query.After != "" && (saved.RecordedAt.After(cursor.RecordedAt) || (saved.RecordedAt.Equal(cursor.RecordedAt) && command >= cursor.Command))) {
			continue
		}
		receipt, readErr := decodeCapacityCommandRead(row.Request, row.Receipt, app, sub, command, now)
		if readErr != nil {
			return out, readErr
		}
		if !receipt.RetainUntil.Equal(row.RetainUntil) {
			return out, fmt.Errorf("capacity command retention metadata changed")
		}
		receipts = append(receipts, receipt)
	}
	sort.Slice(receipts, func(i, j int) bool {
		if !receipts[i].RecordedAt.Equal(receipts[j].RecordedAt) {
			return receipts[i].RecordedAt.After(receipts[j].RecordedAt)
		}
		return receipts[i].CommandID > receipts[j].CommandID
	})
	out = capacityCommandHistoryResult(account, app, sub, query, now, receipts)
	return out, ctx.Err()
}
