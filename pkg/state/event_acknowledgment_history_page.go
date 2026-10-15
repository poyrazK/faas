package state

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventAcknowledgmentHistoryPageStore interface {
	ListEventAcknowledgmentHistoryPage(context.Context, string, string, string, api.EventAcknowledgmentHistoryQuery) (api.EventAcknowledgmentHistoryPage, error)
}
type acknowledgmentHistoryCursor struct {
	Version    int       `json:"v"`
	Scope      string    `json:"scope"`
	Cutoff     time.Time `json:"cutoff"`
	FirstAt    time.Time `json:"first_at"`
	ID         string    `json:"id"`
	Attempt    int       `json:"attempt"`
	Generation int64     `json:"generation"`
	Kind       string    `json:"kind"`
}

func validAcknowledgmentOutcomeKind(kind string) bool {
	switch kind {
	case "accepted", "renewed", "renewal_rejected", "completed", "expired", "retry_requested", "dead_letter_requested":
		return true
	default:
		return false
	}
}
func encodeAcknowledgmentHistoryCursor(c acknowledgmentHistoryCursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func normalizeAcknowledgmentHistoryTime(t *time.Time) (*time.Time, error) {
	if t == nil {
		return nil, nil
	}
	if t.IsZero() || t.Year() < 1 || t.Year() > 9999 {
		return nil, ErrInvalidArgument
	}
	value := t.UTC().Truncate(time.Microsecond)
	if value.Year() < 1 || value.Year() > 9999 {
		return nil, ErrInvalidArgument
	}
	return &value, nil
}
func acknowledgmentHistoryPageQuery(account, app, sub string, q api.EventAcknowledgmentHistoryQuery, now time.Time) (api.EventAcknowledgmentHistoryQuery, acknowledgmentHistoryCursor, error) {
	now = now.UTC().Truncate(time.Microsecond)
	c := acknowledgmentHistoryCursor{Version: 1, Cutoff: now, ID: "00000000-0000-0000-0000-000000000000"}
	if validAcknowledgmentHistoryScope(account, app, sub, q.InvocationID) != nil || (q.Kind != "" && !validAcknowledgmentOutcomeKind(q.Kind)) || len(q.Cursor) > api.EventAcknowledgmentHistoryCursorMaxBytes {
		return q, c, ErrInvalidArgument
	}
	if q.Limit == 0 {
		q.Limit = api.EventAcknowledgmentHistoryLimit
	}
	if q.Limit < 1 || q.Limit > api.EventAcknowledgmentHistoryLimit {
		return q, c, ErrInvalidArgument
	}
	var err error
	q.From, err = normalizeAcknowledgmentHistoryTime(q.From)
	if err != nil {
		return q, c, err
	}
	q.Until, err = normalizeAcknowledgmentHistoryTime(q.Until)
	if err != nil {
		return q, c, err
	}
	if q.From != nil && q.Until != nil && !q.From.Before(*q.Until) {
		return q, c, ErrInvalidArgument
	}
	if q.InvocationID != "" {
		q.InvocationID = canonicalMemUUID(q.InvocationID)
	}
	token := q.Cursor
	q.Cursor = ""
	raw, _ := json.Marshal([]any{canonicalMemUUID(account), canonicalMemUUID(app), canonicalMemUUID(sub), q})
	digest := sha256.Sum256(raw)
	c.Scope = hex.EncodeToString(digest[:])
	q.Cursor = token
	if token == "" {
		return q, c, nil
	}
	raw, err = base64.RawURLEncoding.DecodeString(token)
	var saved acknowledgmentHistoryCursor
	if err != nil || json.Unmarshal(raw, &saved) != nil || encodeAcknowledgmentHistoryCursor(saved) != token || saved.Version != 1 || saved.Scope != c.Scope || saved.Cutoff.IsZero() || saved.Cutoff.After(now) || !saved.Cutoff.Add(api.EventAcknowledgmentHistoryCursorLifetime).After(now) || saved.FirstAt.IsZero() || saved.FirstAt.Year() < 1 || saved.Cutoff.Year() < 1 || saved.Cutoff.Location() != time.UTC || saved.FirstAt.Location() != time.UTC || saved.FirstAt.After(saved.Cutoff) || saved.Attempt < 1 || saved.Attempt > math.MaxInt32 || saved.Generation < 0 || !validAcknowledgmentOutcomeKind(saved.Kind) {
		return q, c, ErrInvalidArgument
	}
	if _, err = operationUUID(saved.ID); err != nil || saved.ID != canonicalMemUUID(saved.ID) {
		return q, c, ErrInvalidArgument
	}
	if (q.From != nil && saved.FirstAt.Before(*q.From)) || (q.Until != nil && !saved.FirstAt.Before(*q.Until)) || (q.Kind != "" && saved.Kind != q.Kind) || (q.InvocationID != "" && saved.ID != q.InvocationID) {
		return q, c, ErrInvalidArgument
	}
	return q, saved, nil
}
func acknowledgmentOutcomeBefore(a, b api.EventAcknowledgmentOutcome) bool {
	if !a.FirstAt.Equal(b.FirstAt) {
		return a.FirstAt.After(b.FirstAt)
	}
	if a.InvocationID != b.InvocationID {
		return a.InvocationID > b.InvocationID
	}
	if a.Attempt != b.Attempt {
		return a.Attempt > b.Attempt
	}
	if a.ReplayGeneration != b.ReplayGeneration {
		return a.ReplayGeneration > b.ReplayGeneration
	}
	return a.Kind > b.Kind
}
func acknowledgmentHistoryPageResult(rows []api.EventAcknowledgmentOutcome, q api.EventAcknowledgmentHistoryQuery, c acknowledgmentHistoryCursor) api.EventAcknowledgmentHistoryPage {
	sort.Slice(rows, func(i, j int) bool { return acknowledgmentOutcomeBefore(rows[i], rows[j]) })
	out := api.EventAcknowledgmentHistoryPage{Outcomes: rows, Cutoff: c.Cutoff, Coverage: "retained_coalesced_deferred_outcomes"}
	if len(rows) > q.Limit {
		out.HasMore = true
		out.Outcomes = rows[:q.Limit]
		last := out.Outcomes[q.Limit-1]
		c.FirstAt = last.FirstAt.UTC()
		c.ID = last.InvocationID
		c.Attempt = last.Attempt
		c.Generation = last.ReplayGeneration
		c.Kind = last.Kind
		out.NextCursor = encodeAcknowledgmentHistoryCursor(c)
	}
	return out
}
func (s *PgStore) ListEventAcknowledgmentHistoryPage(ctx context.Context, account, app, sub string, query api.EventAcknowledgmentHistoryQuery) (api.EventAcknowledgmentHistoryPage, error) {
	if err := validAcknowledgmentHistoryScope(account, app, sub, query.InvocationID); err != nil {
		return api.EventAcknowledgmentHistoryPage{}, err
	}
	if _, err := s.EventAcknowledgmentPolicy(ctx, account, app, sub, nil); err != nil {
		return api.EventAcknowledgmentHistoryPage{}, err
	}
	clock, err := sqlc.New().EventAckNow(ctx, s.pool)
	if err != nil {
		return api.EventAcknowledgmentHistoryPage{}, err
	}
	q, c, err := acknowledgmentHistoryPageQuery(account, app, sub, query, timeFromPgtype(clock))
	if err != nil {
		return api.EventAcknowledgmentHistoryPage{}, err
	}
	rows, err := sqlc.New().EventAckOutcomeHistoryPage(ctx, s.pool, sqlc.EventAckOutcomeHistoryPageParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: canonicalMemUUID(sub), DeliveryID: q.InvocationID, KindFilter: q.Kind, Cutoff: pgtypeFromTime(c.Cutoff), FromAt: nullableTimestamptzPtr(q.From), UntilAt: nullableTimestamptzPtr(q.Until), HasAfter: q.Cursor != "", AfterAt: pgtypeFromTime(c.FirstAt), AfterID: mustPgUUID(c.ID), AfterAttempt: int32(c.Attempt), AfterGeneration: c.Generation, AfterKind: c.Kind, RowLimit: int32(q.Limit + 1)})
	if err != nil {
		return api.EventAcknowledgmentHistoryPage{}, err
	}
	out := make([]api.EventAcknowledgmentOutcome, 0, len(rows))
	for _, r := range rows {
		out = append(out, api.EventAcknowledgmentOutcome{InvocationID: uuidString(r.InvocationID), Attempt: int(r.Attempt), ReplayGeneration: r.ReplayGeneration, Kind: r.Kind, FirstAt: timeFromPgtype(r.FirstAt), LastAt: timeFromPgtype(r.LastAt), Occurrences: r.Occurrences, RetainUntil: timeFromPgtype(r.RetainUntil)})
	}
	return acknowledgmentHistoryPageResult(out, q, c), nil
}
func (m *MemStore) ListEventAcknowledgmentHistoryPage(ctx context.Context, account, app, sub string, query api.EventAcknowledgmentHistoryQuery) (api.EventAcknowledgmentHistoryPage, error) {
	if err := ctx.Err(); err != nil {
		return api.EventAcknowledgmentHistoryPage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	q, c, err := acknowledgmentHistoryPageQuery(account, app, sub, query, time.Now().UTC())
	if err != nil {
		return api.EventAcknowledgmentHistoryPage{}, err
	}
	if m.eventRoutingRetryTargetLocked(account, app, sub) == nil {
		return api.EventAcknowledgmentHistoryPage{}, ErrNotFound
	}
	boundary := api.EventAcknowledgmentOutcome{InvocationID: c.ID, FirstAt: c.FirstAt, Attempt: c.Attempt, ReplayGeneration: c.Generation, Kind: c.Kind}
	now := time.Now()
	rows := []api.EventAcknowledgmentOutcome{}
	for _, row := range m.eventAcknowledgmentOutcomes {
		if err := ctx.Err(); err != nil {
			return api.EventAcknowledgmentHistoryPage{}, err
		}
		inv, ok := m.invocations[row.InvocationID]
		if !ok || !sameMemUUID(inv.AccountID, account) || !sameMemUUID(inv.AppID, app) || !row.RetainUntil.After(now) || row.FirstAt.After(c.Cutoff) || (q.InvocationID != "" && row.InvocationID != q.InvocationID) || (q.Kind != "" && row.Kind != q.Kind) || (q.From != nil && row.FirstAt.Before(*q.From)) || (q.Until != nil && !row.FirstAt.Before(*q.Until)) || (q.Cursor != "" && !acknowledgmentOutcomeBefore(boundary, row)) {
			continue
		}
		var headers map[string]string
		if json.Unmarshal(inv.Headers, &headers) != nil || !sameMemUUID(headers["x-gregale-event-subscription-id"], sub) {
			continue
		}
		rows = append(rows, row)
	}
	return acknowledgmentHistoryPageResult(rows, q, c), nil
}
