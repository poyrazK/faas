package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Coverage describes retained observations, including migration checkpoints;
// neither old, unrecorded transitions nor compacted details can be reconstructed.
const EventRoutingHistoryCoverage = "bounded_recorded_outcomes"

type EventFanoutHistorySummary struct {
	SubscriptionID                                                            string
	ObservedOutcomes, CapacityDeferrals, CoalescedOutcomes, CompactedOutcomes int64
	CompactedThroughID                                                        int64
	CompactedThroughAt, FirstCapacityWaitAt, LastCapacityWaitAt               *time.Time
	LastCapacityScope                                                         string
	RetainedRecords, RetainedBytes                                            int64
}

type EventFanoutHistorySummaryStore interface {
	ListEventFanoutHistorySummariesForApp(context.Context, string, string, string, string) ([]EventFanoutHistorySummary, error)
}

type EventFanoutHistoryRetentionStore interface {
	PruneEventFanoutHistory(context.Context, time.Time, int) (int64, error)
}

type eventHistoryKey struct {
	outboxID       int64
	subscriptionID string
}
type eventHistorySummary struct {
	EventFanoutHistorySummary
	latestID, latestFailureID, latestReplayID int64
	lastOutcomeCapacityScope                  string
}

func eventHistoryWaitScope(action string, p PublishedEventRecipientProgress) string {
	if (action == EventFanoutAttemptActionAttempt || action == EventFanoutAttemptActionBackfill) && p.State == PublishedEventRecipientPending {
		return p.CapacityScope
	}
	return ""
}
func eventHistoryFailure(action string, p PublishedEventRecipientProgress) bool {
	return (action == EventFanoutAttemptActionAttempt || action == EventFanoutAttemptActionBackfill) && p.CapacityScope == "" && (p.FailureCode != "" || p.State == PublishedEventRecipientFailed)
}
func eventHistoryText(s string, limit int) string {
	s = strings.ToValidUTF8(s, "�")
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
func boundedEventHistoryProgress(p PublishedEventRecipientProgress) (PublishedEventRecipientProgress, bool) {
	originalError, originalCode := p.LastError, p.FailureCode
	p.LastError = eventHistoryText(p.LastError, api.EventRoutingHistoryErrorMaxBytes)
	p.FailureCode = eventHistoryText(p.FailureCode, api.EventRoutingHistoryCodeMaxBytes)
	return p, p.LastError != originalError || p.FailureCode != originalCode
}

// All writers already own the parent receipt lock. Summary, detail and
// checkpoint commit together; the final admission fence still runs afterwards.
func recordBoundedEventHistory(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, id int64, app, sub, action string, p PublishedEventRecipientProgress) error {
	scope := eventHistoryWaitScope(action, p)
	coalesced, err := q.EventHistoryObserve(ctx, tx, sqlc.EventHistoryObserveParams{
		OutboxID: id, SubscriptionID: sub, AppID: mustPgUUID(app), CapacityDeferrals: int64(p.CapacityDeferrals), WaitScope: scope, OccurredAt: pgtypeFromTime(p.UpdatedAt),
	})
	if err != nil {
		return err
	}
	if coalesced {
		if action == EventFanoutAttemptActionReplay {
			return nil
		}
		return observeCircuitTx(ctx, q, tx, id, app, sub, p)
	}
	bounded, truncated := boundedEventHistoryProgress(p)
	historyID, err := q.EventRecipientAppendHistory(ctx, tx, sqlc.EventRecipientAppendHistoryParams{
		OutboxID: id, AppID: mustPgUUID(app), SubscriptionID: sub, Action: action, State: p.State, Attempts: int32(p.Attempts),
		FilterReason: p.FilterReason, RetryStopReason: p.RetryStopReason, FailureCode: bounded.FailureCode, Retryable: p.Retryable, LastError: bounded.LastError, OccurredAt: pgtypeFromTime(p.UpdatedAt),
		CapacityScope: scope, CapacityDeferrals: int64(p.CapacityDeferrals), DetailsTruncated: truncated,
	})
	if err != nil {
		return err
	}
	if err := q.EventHistoryMarkDetail(ctx, tx, sqlc.EventHistoryMarkDetailParams{
		HistoryID: historyID, IsFailure: eventHistoryFailure(action, p), IsReplay: action == EventFanoutAttemptActionReplay || action == EventFanoutAttemptActionBackfill, OutboxID: id, SubscriptionID: sub,
	}); err != nil {
		return err
	}
	if err := compactEventHistoryTx(ctx, q, tx, id, sub, time.Now().UTC()); err != nil {
		return err
	}
	if action == EventFanoutAttemptActionReplay {
		return nil
	}
	return observeCircuitTx(ctx, q, tx, id, app, sub, p)
}
func compactEventHistoryTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, id int64, sub string, now time.Time) error {
	if _, err := q.EventHistoryCompact(ctx, tx, sqlc.EventHistoryCompactParams{
		OutboxID: id, SubscriptionID: sub, MaxRows: api.EventRoutingHistoryMaxRows, MaxBytes: api.EventRoutingHistoryMaxBytes,
		BeforeAt: pgtypeFromTime(now.Add(-api.EventRoutingHistoryRetention)),
	}); err != nil {
		return err
	}
	return q.EventHistorySchedulePrune(ctx, tx, sqlc.EventHistorySchedulePruneParams{
		OutboxID: id, SubscriptionID: sub, RetentionSeconds: int64(api.EventRoutingHistoryRetention / time.Second),
	})
}
func (s *PgStore) PruneEventFanoutHistory(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	limit = min(limit, api.EventRoutingHistoryPruneBatch)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck
	q := sqlc.New()
	ids, err := q.EventHistoryPruneCandidates(ctx, tx, sqlc.EventHistoryPruneCandidatesParams{NowAt: pgtypeFromTime(now), BatchLimit: int32(limit)})
	if err != nil {
		return 0, err
	}
	var compacted int64
	seen := make(map[int64]bool)
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		subs, err := q.EventHistoryDueRecipients(ctx, tx, sqlc.EventHistoryDueRecipientsParams{OutboxID: id, NowAt: pgtypeFromTime(now), BatchLimit: int32(limit - int(compacted))})
		if err != nil {
			return 0, err
		}
		for _, sub := range subs {
			if err := compactEventHistoryTx(ctx, q, tx, id, sub, now); err != nil {
				return 0, fmt.Errorf("compact routing history: %w", err)
			}
			compacted++
		}
		if compacted >= int64(limit) {
			break
		}
	}
	return compacted, tx.Commit(ctx)
}
func (s *PgStore) listEventHistory(ctx context.Context, app string, limit int, before EventFanoutAttemptCursor, source, event, sub string) ([]EventFanoutAttempt, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := sqlc.New().EventHistoryList(ctx, s.pool, sqlc.EventHistoryListParams{
		AppID: mustPgUUID(app), EventSource: source, EventID: event, SubscriptionID: sub, BeforeID: before.ID, PageLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]EventFanoutAttempt, 0, len(rows))
	for _, r := range rows {
		out = append(out, EventFanoutAttempt{
			ID: r.ID, OutboxID: r.OutboxID, AppID: uuidString(r.AppID), EventID: r.EventID, EventSource: r.EventSource, EventType: r.EventType,
			SubscriptionID: r.SubscriptionID, Action: r.Action, State: r.State, Attempts: int(r.Attempts), FailureCode: r.FailureCode, Retryable: r.Retryable,
			FilterReason: r.FilterReason, RetryStopReason: r.RetryStopReason, LastError: r.LastError, OccurredAt: timeFromPgtype(r.OccurredAt), CapacityScope: r.CapacityScope, CapacityDeferrals: r.CapacityDeferrals,
			DetailsTruncated: r.DetailsTruncated, HistoryBytes: r.HistoryBytes,
		})
	}
	return out, nil
}
func (s *PgStore) ListEventFanoutHistorySummariesForApp(ctx context.Context, app, source, event, sub string) ([]EventFanoutHistorySummary, error) {
	rows, err := sqlc.New().EventHistorySummaries(ctx, s.pool, sqlc.EventHistorySummariesParams{AppID: mustPgUUID(app), EventSource: source, EventID: event, SubscriptionID: sub})
	if err != nil {
		return nil, err
	}
	out := make([]EventFanoutHistorySummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, EventFanoutHistorySummary{
			SubscriptionID: r.SubscriptionID, ObservedOutcomes: r.ObservedOutcomes, CapacityDeferrals: r.CapacityDeferrals,
			CoalescedOutcomes: r.CoalescedOutcomes, CompactedOutcomes: r.CompactedOutcomes, CompactedThroughID: r.CompactedThroughID,
			CompactedThroughAt: eventHistoryPgTimePointer(r.CompactedThroughAt), FirstCapacityWaitAt: eventHistoryPgTimePointer(r.FirstCapacityWaitAt),
			LastCapacityWaitAt: eventHistoryPgTimePointer(r.LastCapacityWaitAt), LastCapacityScope: r.LastCapacityScope,
			RetainedRecords: r.RetainedRecords, RetainedBytes: r.RetainedBytes,
		})
	}
	return out, nil
}

func (m *MemStore) recordEventFanoutHistoryLocked(work *PublishedEventWork, app string, event publishedEventIdentity, sub, action string, p PublishedEventRecipientProgress) {
	if m.eventFanoutHistorySummaries == nil {
		m.eventFanoutHistorySummaries = make(map[eventHistoryKey]*eventHistorySummary)
	}
	key := eventHistoryKey{work.ID, sub}
	summary := m.eventFanoutHistorySummaries[key]
	if summary == nil {
		summary = &eventHistorySummary{EventFanoutHistorySummary: EventFanoutHistorySummary{SubscriptionID: sub}}
		m.eventFanoutHistorySummaries[key] = summary
	}
	scope := eventHistoryWaitScope(action, p)
	coalesced := scope != "" && summary.lastOutcomeCapacityScope == scope
	summary.ObservedOutcomes++
	summary.CapacityDeferrals = max(summary.CapacityDeferrals, int64(p.CapacityDeferrals))
	if scope != "" {
		at := p.UpdatedAt
		if summary.FirstCapacityWaitAt == nil {
			summary.FirstCapacityWaitAt = &at
		}
		summary.LastCapacityWaitAt = &at
		summary.LastCapacityScope = scope
	}
	summary.lastOutcomeCapacityScope = scope
	if coalesced {
		summary.CoalescedOutcomes++
		if action != EventFanoutAttemptActionReplay {
			m.observeCircuitLocked(work, sub, p)
		}
		return
	}
	p, truncated := boundedEventHistoryProgress(p)
	m.eventFanoutAttemptNextID++
	id := m.eventFanoutAttemptNextID
	a := EventFanoutAttempt{ID: id, OutboxID: work.ID, AppID: app, EventID: event.ID, EventSource: event.Source, EventType: event.Type,
		SubscriptionID: sub, Action: action, State: p.State, Attempts: p.Attempts, FailureCode: p.FailureCode, Retryable: p.Retryable,
		FilterReason: p.FilterReason, RetryStopReason: p.RetryStopReason, LastError: p.LastError, OccurredAt: p.UpdatedAt, CapacityScope: scope, CapacityDeferrals: int64(p.CapacityDeferrals), DetailsTruncated: truncated}
	a.HistoryBytes = int64(api.EventRoutingHistoryRowOverheadBytes + len(sub) + len(action) + len(p.State) + len(p.FailureCode) + len(p.LastError) + len(scope) + len(p.RetryStopReason) + len(p.FilterReason))
	m.eventFanoutAttempts = append(m.eventFanoutAttempts, a)
	summary.latestID = id
	if eventHistoryFailure(action, p) {
		summary.latestFailureID = id
	}
	if action == EventFanoutAttemptActionReplay || action == EventFanoutAttemptActionBackfill {
		summary.latestReplayID = id
	}
	m.compactEventHistoryLocked(key, summary, time.Now().UTC())
	if action != EventFanoutAttemptActionReplay {
		m.observeCircuitLocked(work, sub, p)
	}
}
func eventHistoryProtected(s *eventHistorySummary, id int64) bool {
	return id == s.latestID || id == s.latestFailureID || id == s.latestReplayID
}
func (m *MemStore) compactEventHistoryLocked(key eventHistoryKey, s *eventHistorySummary, now time.Time) {
	rows := make([]EventFanoutAttempt, 0)
	for _, a := range m.eventFanoutAttempts {
		if a.OutboxID == key.outboxID && a.SubscriptionID == key.subscriptionID {
			rows = append(rows, a)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		pi, pj := eventHistoryProtected(s, rows[i].ID), eventHistoryProtected(s, rows[j].ID)
		if pi != pj {
			return pi
		}
		return rows[i].ID > rows[j].ID
	})
	removed := make(map[int64]bool)
	var bytes int64
	s.RetainedRecords, s.RetainedBytes = 0, 0
	for i, a := range rows {
		bytes += a.HistoryBytes
		if i >= api.EventRoutingHistoryMaxRows || bytes > api.EventRoutingHistoryMaxBytes ||
			(!eventHistoryProtected(s, a.ID) && !a.OccurredAt.After(now.Add(-api.EventRoutingHistoryRetention))) {
			removed[a.ID] = true
			s.CompactedOutcomes++
			s.CompactedThroughID = max(s.CompactedThroughID, a.ID)
			if s.CompactedThroughAt == nil || a.OccurredAt.After(*s.CompactedThroughAt) {
				at := a.OccurredAt
				s.CompactedThroughAt = &at
			}
		} else {
			s.RetainedRecords++
			s.RetainedBytes += a.HistoryBytes
		}
	}
	out := m.eventFanoutAttempts[:0]
	for _, a := range m.eventFanoutAttempts {
		if !removed[a.ID] {
			out = append(out, a)
		}
	}
	m.eventFanoutAttempts = out
}
func (m *MemStore) PruneEventFanoutHistory(_ context.Context, now time.Time, limit int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		return 0, nil
	}
	limit = min(limit, api.EventRoutingHistoryPruneBatch)
	var n int64
	for key, s := range m.eventFanoutHistorySummaries {
		due := false
		for _, a := range m.eventFanoutAttempts {
			if a.OutboxID == key.outboxID && a.SubscriptionID == key.subscriptionID && !eventHistoryProtected(s, a.ID) && !a.OccurredAt.After(now.Add(-api.EventRoutingHistoryRetention)) {
				due = true
				break
			}
		}
		if !due {
			continue
		}
		m.compactEventHistoryLocked(key, s, now)
		n++
		if n >= int64(limit) {
			break
		}
	}
	return n, nil
}
func (m *MemStore) ListEventFanoutHistorySummariesForApp(_ context.Context, app, source, event, sub string) ([]EventFanoutHistorySummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]EventFanoutHistorySummary, 0)
	currentApp, exists := m.eventSubscriptionAppLocked(app)
	if !exists {
		return out, nil
	}
	for _, work := range m.eventFanout {
		var identity publishedEventIdentity
		if err := json.Unmarshal(work.Payload, &identity); err != nil || (source != "" && identity.Source != source) || (event != "" && identity.ID != event) {
			continue
		}
		for _, r := range work.RecipientSnapshot {
			if !sameMemUUID(r.AppID, app) || !sameMemUUID(r.AccountID, currentApp.AccountID) || (sub != "" && r.ID != sub) {
				continue
			}
			if s := m.eventFanoutHistorySummaries[eventHistoryKey{work.ID, r.ID}]; s != nil {
				copy := s.EventFanoutHistorySummary
				copy.CompactedThroughAt = cloneEventHistoryTime(copy.CompactedThroughAt)
				copy.FirstCapacityWaitAt = cloneEventHistoryTime(copy.FirstCapacityWaitAt)
				copy.LastCapacityWaitAt = cloneEventHistoryTime(copy.LastCapacityWaitAt)
				out = append(out, copy)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SubscriptionID < out[j].SubscriptionID })
	return out, nil
}
func cloneEventHistoryTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}

func eventHistoryPgTimePointer(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := timeFromPgtype(t)
	return &v
}
