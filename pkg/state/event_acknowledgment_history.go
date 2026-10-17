package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventAcknowledgmentHistoryStore interface {
	ListEventAcknowledgmentHistory(context.Context, string, string, string, string) (api.EventAcknowledgmentHistory, error)
}
type eventAcknowledgmentOutcomeKey struct {
	id    string
	claim InvocationClaim
	kind  string
}

func acknowledgmentHistory(rows []api.EventAcknowledgmentOutcome) api.EventAcknowledgmentHistory {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if !a.LastAt.Equal(b.LastAt) {
			return a.LastAt.After(b.LastAt)
		}
		if a.InvocationID != b.InvocationID {
			return a.InvocationID < b.InvocationID
		}
		if a.Attempt != b.Attempt {
			return a.Attempt < b.Attempt
		}
		if a.ReplayGeneration != b.ReplayGeneration {
			return a.ReplayGeneration < b.ReplayGeneration
		}
		return a.Kind < b.Kind
	})
	truncated := len(rows) > api.EventAcknowledgmentHistoryLimit
	if truncated {
		rows = rows[:api.EventAcknowledgmentHistoryLimit]
	}
	return api.EventAcknowledgmentHistory{Outcomes: rows, Truncated: truncated, Coverage: "retained_coalesced_deferred_outcomes"}
}
func validAcknowledgmentHistoryScope(account, app, sub, id string) error {
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return err
	}
	if id != "" {
		if _, err := operationUUID(id); err != nil {
			return ErrInvalidArgument
		}
	}
	return nil
}
func recordAcknowledgmentOutcome(ctx context.Context, tx pgx.Tx, id, kind string) error {
	if err := sqlc.New().EventAckOutcomeRecord(ctx, tx, sqlc.EventAckOutcomeRecordParams{InvocationID: mustPgUUID(id), Kind: kind, RetentionSeconds: int32(api.EventAcknowledgmentHistoryRetention / time.Second)}); err != nil {
		return err
	}
	if !acknowledgmentMetricKind(kind) {
		return nil
	}
	return sqlc.New().EventAckMetricRecord(ctx, tx, sqlc.EventAckMetricRecordParams{InvocationID: mustPgUUID(id), Kind: kind, RetentionSeconds: int32(api.EventAcknowledgmentMetricRetention / time.Second)})
}

// Rejections commit only after the current token and attempt have been verified.
func rejectAcknowledgmentRenewal(ctx context.Context, tx pgx.Tx, id, action string) (api.EventAcknowledgmentStatus, error) {
	if action == "renew" {
		if err := recordAcknowledgmentOutcome(ctx, tx, id, "renewal_rejected"); err != nil {
			return api.EventAcknowledgmentStatus{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return api.EventAcknowledgmentStatus{}, err
		}
	}
	return api.EventAcknowledgmentStatus{}, ErrEventAcknowledgmentStale
}
func (s *PgStore) ListEventAcknowledgmentHistory(ctx context.Context, account, app, sub, id string) (api.EventAcknowledgmentHistory, error) {
	if err := validAcknowledgmentHistoryScope(account, app, sub, id); err != nil {
		return api.EventAcknowledgmentHistory{}, err
	}
	// Policy lookup enforces ownership and existing consumer scope, including empty histories.
	if _, err := s.EventAcknowledgmentPolicy(ctx, account, app, sub, nil); err != nil {
		return api.EventAcknowledgmentHistory{}, err
	}
	if id != "" {
		id = canonicalMemUUID(id)
	}
	rows, err := sqlc.New().EventAckOutcomeHistory(ctx, s.pool, sqlc.EventAckOutcomeHistoryParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: canonicalMemUUID(sub), DeliveryID: id, RowLimit: int32(api.EventAcknowledgmentHistoryLimit + 1)})
	if err != nil {
		return api.EventAcknowledgmentHistory{}, err
	}
	out := make([]api.EventAcknowledgmentOutcome, 0, len(rows))
	for _, r := range rows {
		out = append(out, api.EventAcknowledgmentOutcome{InvocationID: uuidString(r.InvocationID), Attempt: int(r.Attempt), ReplayGeneration: r.ReplayGeneration, Kind: r.Kind, FirstAt: timeFromPgtype(r.FirstAt), LastAt: timeFromPgtype(r.LastAt), Occurrences: r.Occurrences, RetainUntil: timeFromPgtype(r.RetainUntil)})
	}
	return acknowledgmentHistory(out), nil
}
func (m *MemStore) recordAcknowledgmentOutcomeLocked(inv Invocation, ack eventAcknowledgment, kind string, now time.Time) {
	m.recordAcknowledgmentMetricLocked(inv, kind, now)
	until := ack.deadline.Add(api.EventAcknowledgmentHistoryRetention)
	if !until.After(now) {
		return
	}
	if m.eventAcknowledgmentOutcomes == nil {
		m.eventAcknowledgmentOutcomes = map[eventAcknowledgmentOutcomeKey]api.EventAcknowledgmentOutcome{}
	}
	key := eventAcknowledgmentOutcomeKey{inv.ID, ack.claim, kind}
	row, ok := m.eventAcknowledgmentOutcomes[key]
	if !ok {
		row = api.EventAcknowledgmentOutcome{InvocationID: inv.ID, Attempt: ack.claim.Attempt, ReplayGeneration: ack.claim.ReplayGeneration, Kind: kind, FirstAt: now, RetainUntil: until}
	}
	row.LastAt = now
	if row.Occurrences < 2147483647 {
		row.Occurrences++
	}
	m.eventAcknowledgmentOutcomes[key] = row
}
func (m *MemStore) rejectAcknowledgmentRenewalLocked(inv Invocation, ack eventAcknowledgment, action string, now time.Time) (api.EventAcknowledgmentStatus, error) {
	if action == "renew" {
		m.recordAcknowledgmentOutcomeLocked(inv, ack, "renewal_rejected", now)
	}
	return api.EventAcknowledgmentStatus{}, ErrEventAcknowledgmentStale
}
func (m *MemStore) ListEventAcknowledgmentHistory(ctx context.Context, account, app, sub, id string) (api.EventAcknowledgmentHistory, error) {
	if err := ctx.Err(); err != nil {
		return api.EventAcknowledgmentHistory{}, err
	}
	if err := validAcknowledgmentHistoryScope(account, app, sub, id); err != nil {
		return api.EventAcknowledgmentHistory{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.eventRoutingRetryTargetLocked(account, app, sub) == nil {
		return api.EventAcknowledgmentHistory{}, ErrNotFound
	}
	now := time.Now()
	out := []api.EventAcknowledgmentOutcome{}
	for _, row := range m.eventAcknowledgmentOutcomes {
		inv, ok := m.invocations[row.InvocationID]
		if !ok || !sameMemUUID(inv.AccountID, account) || !sameMemUUID(inv.AppID, app) || !row.RetainUntil.After(now) || (id != "" && !sameMemUUID(id, inv.ID)) {
			continue
		}
		var headers map[string]string
		if json.Unmarshal(inv.Headers, &headers) != nil || !sameMemUUID(headers["x-gregale-event-subscription-id"], sub) {
			continue
		}
		out = append(out, row)
	}
	return acknowledgmentHistory(out), nil
}

func acknowledgmentOutcomeKind(action string) string {
	switch action {
	case "accept":
		return "accepted"
	case "renew":
		return "renewed"
	default:
		return "completed"
	}
}
