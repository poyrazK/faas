package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrSubscriptionCapacityCommandConflict = errors.New("state: capacity command UUID reused with different inputs")
var ErrSubscriptionCapacityCommandLimit = errors.New("state: capacity command receipt limit reached")

type memSubscriptionCapacityCommand struct {
	AccountID, AppID, SubscriptionID string
	Request, Receipt                 []byte
	RetainUntil                      time.Time
}

func normalizeCapacityCommandRequest(req api.EventSubscriptionCapacityRequest) (api.EventSubscriptionCapacityRequest, []byte, error) {
	if err := req.Validate(); err != nil {
		return req, nil, ErrInvalidArgument
	}
	req.CommandID = strings.ToLower(req.CommandID)
	payload, err := json.Marshal(req)
	var cloned api.EventSubscriptionCapacityRequest
	if err == nil {
		err = json.Unmarshal(payload, &cloned)
	}
	return cloned, payload, err
}
func capacityCommandRequestMatches(stored, payload []byte) bool {
	var original api.EventSubscriptionCapacityRequest
	if json.Unmarshal(stored, &original) != nil {
		return false
	}
	_, canonical, err := normalizeCapacityCommandRequest(original)
	return err == nil && bytes.Equal(canonical, payload)
}
func capacityCommandReceipt(req api.EventSubscriptionCapacityRequest, app, sub string, previous, ceiling int, now time.Time) (*api.EventSubscriptionCapacityCommandReceipt, []byte, error) {
	if req.CommandID == "" {
		return nil, nil, nil
	}
	receipt := &api.EventSubscriptionCapacityCommandReceipt{CommandID: req.CommandID, AppID: canonicalMemUUID(app), SubscriptionID: canonicalMemUUID(sub), RequestedMaxPendingDeliveries: *req.MaxPendingDeliveries, Expected: req.Expected, PreviousMaxPendingDeliveries: previous, PlanMaxPendingDeliveries: ceiling, EffectiveMaxPendingDeliveries: effectiveEventConsumerCapacity(ceiling, *req.MaxPendingDeliveries), Outcome: "applied", RecordedAt: now, RetainUntil: now.Add(api.EventSubscriptionCapacityCommandRetention)}
	if previous == *req.MaxPendingDeliveries {
		receipt.Outcome = "unchanged"
	}
	raw, err := json.Marshal(receipt)
	return receipt, raw, err
}
func decodeCapacityCommandReceipt(raw []byte, req api.EventSubscriptionCapacityRequest, app, sub string) (*api.EventSubscriptionCapacityCommandReceipt, error) {
	var receipt api.EventSubscriptionCapacityCommandReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return nil, err
	}
	if receipt.CommandID != req.CommandID || !sameMemUUID(receipt.AppID, app) || !sameMemUUID(receipt.SubscriptionID, sub) || receipt.RequestedMaxPendingDeliveries != *req.MaxPendingDeliveries || receipt.PreviousMaxPendingDeliveries < 0 || receipt.PlanMaxPendingDeliveries <= 0 || receipt.RequestedMaxPendingDeliveries > receipt.PlanMaxPendingDeliveries || receipt.EffectiveMaxPendingDeliveries != effectiveEventConsumerCapacity(receipt.PlanMaxPendingDeliveries, receipt.RequestedMaxPendingDeliveries) || receipt.RecordedAt.IsZero() || receipt.RetainUntil.Sub(receipt.RecordedAt) != api.EventSubscriptionCapacityCommandRetention {
		return nil, fmt.Errorf("invalid retained capacity command receipt")
	}
	expectedOutcome := "applied"
	if receipt.PreviousMaxPendingDeliveries == receipt.RequestedMaxPendingDeliveries {
		expectedOutcome = "unchanged"
	}
	check := req
	check.Expected = receipt.Expected
	_, saved, err := normalizeCapacityCommandRequest(check)
	_, original, originalErr := normalizeCapacityCommandRequest(req)
	if receipt.Outcome != expectedOutcome || err != nil || originalErr != nil || !bytes.Equal(saved, original) {
		return nil, fmt.Errorf("invalid original capacity command outcome")
	}
	return &receipt, nil
}
func lookupCapacityCommandTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account, app, sub string, req api.EventSubscriptionCapacityRequest, payload []byte, now time.Time) (*api.EventSubscriptionCapacityCommandReceipt, error) {
	if req.CommandID == "" {
		return nil, nil
	}
	if err := q.EventSubscriptionCapacityCommandsExpire(ctx, tx, sqlc.EventSubscriptionCapacityCommandsExpireParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), NowAt: pgtypeFromTime(now)}); err != nil {
		return nil, err
	}
	row, err := q.EventSubscriptionCapacityCommandGet(ctx, tx, sqlc.EventSubscriptionCapacityCommandGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), CommandID: mustPgUUID(req.CommandID), NowAt: pgtypeFromTime(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !capacityCommandRequestMatches(row.Request, payload) {
		return nil, ErrSubscriptionCapacityCommandConflict
	}
	receipt, err := decodeCapacityCommandReceipt(row.Receipt, req, app, sub)
	if err == nil && (!receipt.RecordedAt.Equal(timeFromPgtype(row.RecordedAt)) || !receipt.RetainUntil.Equal(timeFromPgtype(row.RetainUntil))) {
		err = fmt.Errorf("capacity command retention metadata changed")
	}
	return receipt, err
}
func (m *MemStore) lookupCapacityCommandLocked(account, app, sub string, req api.EventSubscriptionCapacityRequest, payload []byte, now time.Time) (*api.EventSubscriptionCapacityCommandReceipt, error) {
	if req.CommandID == "" {
		return nil, nil
	}
	row, ok := m.eventSubscriptionCapacityCommands[canonicalMemUUID(sub)+"/"+req.CommandID]
	if !ok || !row.RetainUntil.After(now) || !sameMemUUID(row.AccountID, account) || !sameMemUUID(row.AppID, app) {
		return nil, nil
	}
	if !capacityCommandRequestMatches(row.Request, payload) {
		return nil, ErrSubscriptionCapacityCommandConflict
	}
	return decodeCapacityCommandReceipt(row.Receipt, req, app, sub)
}
func (m *MemStore) capacityCommandCountLocked(account, app, sub string, now time.Time) int {
	count := 0
	for _, row := range m.eventSubscriptionCapacityCommands {
		if row.RetainUntil.After(now) && sameMemUUID(row.AccountID, account) && sameMemUUID(row.AppID, app) && sameMemUUID(row.SubscriptionID, sub) {
			count++
		}
	}
	return count
}

type EventSubscriptionCapacityCommandRetentionStore interface {
	PruneEventSubscriptionCapacityCommands(context.Context, time.Time) (int, error)
}

func (s *PgStore) PruneEventSubscriptionCapacityCommands(ctx context.Context, now time.Time) (int, error) {
	if now.IsZero() {
		return 0, ErrInvalidArgument
	}
	count, err := sqlc.New().PruneEventSubscriptionCapacityCommands(ctx, s.pool, sqlc.PruneEventSubscriptionCapacityCommandsParams{NowAt: pgtypeFromTime(now), BatchLimit: api.EventSubscriptionCapacityCommandPruneBatch})
	return int(count), err
}
func (m *MemStore) PruneEventSubscriptionCapacityCommands(ctx context.Context, now time.Time) (int, error) {
	if now.IsZero() {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := []string{}
	for key, row := range m.eventSubscriptionCapacityCommands {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		app, exists := m.eventSubscriptionAppLocked(row.AppID)
		if !row.RetainUntil.After(now) || !exists || app.Status == AppDeleted || m.eventSubscriptionControls[canonicalMemUUID(row.SubscriptionID)] == nil {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := m.eventSubscriptionCapacityCommands[keys[i]], m.eventSubscriptionCapacityCommands[keys[j]]
		if !left.RetainUntil.Equal(right.RetainUntil) {
			return left.RetainUntil.Before(right.RetainUntil)
		}
		return keys[i] < keys[j]
	})
	if len(keys) > api.EventSubscriptionCapacityCommandPruneBatch {
		keys = keys[:api.EventSubscriptionCapacityCommandPruneBatch]
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	for _, key := range keys {
		delete(m.eventSubscriptionCapacityCommands, key)
	}
	return len(keys), nil
}
