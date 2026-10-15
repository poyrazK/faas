package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Reads never prune receipts, reserve a UUID, or change delivery controls.
type EventSubscriptionCapacityCommandReadStore interface {
	GetEventSubscriptionCapacityCommand(context.Context, string, string, string, string) (api.EventSubscriptionCapacityCommandReceipt, error)
}

var _ EventSubscriptionCapacityCommandReadStore = (*PgStore)(nil)
var _ EventSubscriptionCapacityCommandReadStore = (*MemStore)(nil)

func validateCapacityCommandRead(account, app, sub, command string) error {
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return err
	}
	zero := 0
	if command == "" || (api.EventSubscriptionCapacityRequest{CommandID: command, MaxPendingDeliveries: &zero}).Validate() != nil {
		return ErrInvalidArgument
	}
	return nil
}
func decodeCapacityCommandRead(request, receipt []byte, app, sub, command string, now time.Time) (api.EventSubscriptionCapacityCommandReceipt, error) {
	var req api.EventSubscriptionCapacityRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return api.EventSubscriptionCapacityCommandReceipt{}, err
	}
	req, _, err := normalizeCapacityCommandRequest(req)
	if err != nil || req.CommandID != command {
		return api.EventSubscriptionCapacityCommandReceipt{}, fmt.Errorf("invalid retained capacity command request")
	}
	out, err := decodeCapacityCommandReceipt(receipt, req, app, sub)
	if err != nil {
		return api.EventSubscriptionCapacityCommandReceipt{}, err
	}
	if out.Expected != nil && !eventSubscriptionCapacityGuardMatches(out.Expected, out.PreviousMaxPendingDeliveries, out.PlanMaxPendingDeliveries) {
		return api.EventSubscriptionCapacityCommandReceipt{}, fmt.Errorf("invalid original capacity command expectations")
	}
	if !out.RetainUntil.After(now) || out.RecordedAt.After(now) {
		return api.EventSubscriptionCapacityCommandReceipt{}, fmt.Errorf("invalid retained capacity command time")
	}
	return *out, nil
}
func (s *PgStore) GetEventSubscriptionCapacityCommand(ctx context.Context, account, app, sub, command string) (api.EventSubscriptionCapacityCommandReceipt, error) {
	var out api.EventSubscriptionCapacityCommandReceipt
	if err := validateCapacityCommandRead(account, app, sub, command); err != nil {
		return out, err
	}
	command = strings.ToLower(command)
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
	row, err := q.EventSubscriptionCapacityCommandGet(ctx, tx, sqlc.EventSubscriptionCapacityCommandGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), CommandID: mustPgUUID(command), NowAt: pgtypeFromTime(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out, err = decodeCapacityCommandRead(row.Request, row.Receipt, app, sub, command, now)
	if err != nil {
		return out, err
	}
	if !out.RecordedAt.Equal(timeFromPgtype(row.RecordedAt)) || !out.RetainUntil.Equal(timeFromPgtype(row.RetainUntil)) {
		return api.EventSubscriptionCapacityCommandReceipt{}, fmt.Errorf("capacity command retention metadata changed")
	}
	return out, tx.Commit(ctx)
}
func (m *MemStore) GetEventSubscriptionCapacityCommand(ctx context.Context, account, app, sub, command string) (api.EventSubscriptionCapacityCommandReceipt, error) {
	var out api.EventSubscriptionCapacityCommandReceipt
	if err := validateCapacityCommandRead(account, app, sub, command); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	command = strings.ToLower(command)
	now := time.Now().UTC().Truncate(time.Microsecond)
	row, ok := m.eventSubscriptionCapacityCommands[canonicalMemUUID(sub)+"/"+command]
	if !ok || !row.RetainUntil.After(now) || !sameMemUUID(row.AccountID, account) || !sameMemUUID(row.AppID, app) || !sameMemUUID(row.SubscriptionID, sub) {
		return out, ErrNotFound
	}
	out, err := decodeCapacityCommandRead(row.Request, row.Receipt, app, sub, command, now)
	if err == nil && !out.RetainUntil.Equal(row.RetainUntil) {
		return api.EventSubscriptionCapacityCommandReceipt{}, fmt.Errorf("capacity command retention metadata changed")
	}
	if err == nil {
		err = ctx.Err()
	}
	return out, err
}
