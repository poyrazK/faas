package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventReplayPreviewStore interface {
	PreviewEventReplay(context.Context, string, EventReplayPreviewQuery) (api.EventReplayPreviewResponse, error)
}

type EventReplayPreviewQuery struct {
	AppID          string
	SubscriptionID string
	api.EventReplayPreviewOptions
}

var (
	ErrEventReplayPreviewQuery       = errors.New("invalid event replay preview query")
	ErrEventReplayPreviewChanged     = errors.New("subscription changed; restart the replay preview")
	ErrEventReplayPreviewDisabled    = errors.New("replay preview requires an enabled subscription")
	ErrEventReplayPreviewUnsupported = errors.New("replay preview does not support work-bound subscriptions")
)

const eventReplayPreviewCursorPrefix = "erp1."

type eventReplayPreviewCursor struct {
	Version        int       `json:"v"`
	AccountID      string    `json:"account_id"`
	AppID          string    `json:"app_id"`
	SubscriptionID string    `json:"subscription_id"`
	Revision       string    `json:"revision"`
	From           time.Time `json:"from"`
	Until          time.Time `json:"until"`
	CutoffAt       time.Time `json:"cutoff_at"`
	AcceptedAt     time.Time `json:"accepted_at"`
	OutboxID       int64     `json:"outbox_id"`
}

type retainedEventCandidate struct {
	id                int64
	acceptedAt        time.Time
	payload           []byte
	originalRecipient string
}

func validateEventReplayPreviewQuery(accountID string, q *EventReplayPreviewQuery) error {
	for _, id := range []string{accountID, q.AppID, q.SubscriptionID} {
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("%w: account, app and subscription identifiers must be UUIDs", ErrEventReplayPreviewQuery)
		}
	}
	q.AppID, q.SubscriptionID = canonicalMemUUID(q.AppID), canonicalMemUUID(q.SubscriptionID)
	q.From, q.Until = q.From.UTC(), q.Until.UTC()
	if err := q.EventReplayPreviewOptions.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrEventReplayPreviewQuery, err)
	}
	if q.Limit == 0 {
		q.Limit = api.EventReplayPreviewPageDefault
	}
	return nil
}

func eventReplayPreviewRevision(s EventSubscription) string {
	// The canonical filter, identity and timestamps bind a continuation to the
	// current declaration, including disable/re-enable and delete/recreate.
	var value any
	decoder := json.NewDecoder(bytes.NewReader(s.Filter))
	decoder.UseNumber()
	_ = decoder.Decode(&value)
	filter, _ := json.Marshal(value)
	data, _ := json.Marshal(struct {
		ID, Source, Type string
		Filter           json.RawMessage
		Created, Updated time.Time
	}{s.ID, s.Source, s.Type, filter, s.CreatedAt, s.UpdatedAt})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func eventReplayPreviewWindow(accountID string, q EventReplayPreviewQuery, sub EventSubscription, workBound bool, now time.Time) (eventReplayPreviewCursor, error) {
	if !sub.Enabled {
		return eventReplayPreviewCursor{}, ErrEventReplayPreviewDisabled
	}
	if workBound {
		return eventReplayPreviewCursor{}, ErrEventReplayPreviewUnsupported
	}
	matcher := eventcontract.Subscription{AccountID: sub.AccountID, Source: sub.Source, Type: sub.Type, Filter: sub.Filter}
	if err := matcher.Validate(); err != nil {
		return eventReplayPreviewCursor{}, fmt.Errorf("validate retained preview target: %w", err)
	}
	if err := eventcontract.ValidateFilter(sub.Filter); err != nil {
		return eventReplayPreviewCursor{}, fmt.Errorf("validate retained preview filter: %w", err)
	}
	c := eventReplayPreviewCursor{Version: 1, AccountID: canonicalMemUUID(accountID), AppID: q.AppID, SubscriptionID: q.SubscriptionID, Revision: eventReplayPreviewRevision(sub), From: q.From, Until: q.Until, CutoffAt: minTime(now, q.Until)}
	if !q.From.Before(c.CutoffAt) {
		return c, fmt.Errorf("%w: from must precede the current acceptance cutoff", ErrEventReplayPreviewQuery)
	}
	if q.After == "" {
		return c, nil
	}
	invalid := fmt.Errorf("%w: invalid or mismatched replay preview cursor", ErrEventReplayPreviewQuery)
	if !strings.HasPrefix(q.After, eventReplayPreviewCursorPrefix) {
		return c, invalid
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(q.After, eventReplayPreviewCursorPrefix))
	var prior eventReplayPreviewCursor
	if err != nil || json.Unmarshal(data, &prior) != nil || prior.Version != 1 || prior.AccountID != c.AccountID || prior.AppID != c.AppID || prior.SubscriptionID != c.SubscriptionID || !prior.From.Equal(q.From) || !prior.Until.Equal(q.Until) || prior.CutoffAt.IsZero() || prior.AcceptedAt.IsZero() || len(prior.Revision) != sha256.Size*2 || prior.CutoffAt.After(c.CutoffAt) || !q.From.Before(prior.CutoffAt) || prior.OutboxID <= 0 || prior.AcceptedAt.Before(q.From) || !prior.AcceptedAt.Before(prior.CutoffAt) {
		return c, invalid
	}
	if prior.Revision != c.Revision {
		return c, ErrEventReplayPreviewChanged
	}
	return prior, nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// PostgreSQL stores microseconds. Rounding either range boundary upward keeps
// >= from and < cutoff exact for retained timestamps even with nanosecond input.
func eventReplayPreviewPgBoundary(t time.Time) pgtype.Timestamptz {
	boundary := t.Truncate(time.Microsecond)
	if boundary.Before(t) {
		boundary = boundary.Add(time.Microsecond)
	}
	return pgtype.Timestamptz{Time: boundary, Valid: true}
}

func newEventReplayPreview(sub EventSubscription, slug string, c eventReplayPreviewCursor, now time.Time, earliest *time.Time) api.EventReplayPreviewResponse {
	return api.EventReplayPreviewResponse{
		AppSlug: slug, Subscription: api.EventSubscriptionResponse{ID: sub.ID, AppID: sub.AppID, Source: sub.Source, Type: sub.Type, Filter: append(json.RawMessage(nil), sub.Filter...), Enabled: sub.Enabled, CreatedAt: sub.CreatedAt, UpdatedAt: sub.UpdatedAt},
		SubscriptionRevision: c.Revision, From: c.From, Until: c.Until, CutoffAt: c.CutoffAt, ObservedAt: now, Coverage: "retained_envelopes",
		Retention: api.EventReplayPreviewRetention{SettledRetentionSeconds: int64(PublishedEventIdentityRetention / time.Second), EarliestRetainedAt: earliest},
		Matches:   []api.EventReplayPreviewMatch{},
	}
}

func evaluateEventReplayPreview(ctx context.Context, out api.EventReplayPreviewResponse, sub EventSubscription, c eventReplayPreviewCursor, candidates []retainedEventCandidate, limit int) (api.EventReplayPreviewResponse, error) {
	hasMore := len(candidates) > limit
	if hasMore {
		candidates = candidates[:limit]
	}
	matcher := eventcontract.Subscription{ID: sub.ID, AccountID: sub.AccountID, Source: sub.Source, Type: sub.Type, Filter: sub.Filter}
	for _, row := range candidates {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		var envelope eventcontract.Envelope
		if err := json.Unmarshal(row.payload, &envelope); err != nil {
			return out, fmt.Errorf("decode retained event: %w", err)
		}
		// Historical snake_case envelopes use the same normalization as routing;
		// acceptance time supplies missing legacy time, never the wall clock.
		envelope, err := envelope.Normalize(sub.AccountID, row.acceptedAt)
		if err != nil {
			return out, fmt.Errorf("normalize retained event: %w", err)
		}
		reason, err := matcher.ExplainMatch(envelope)
		if err != nil {
			return out, fmt.Errorf("match retained event: %w", err)
		}
		out.ScannedCount++
		switch reason {
		case eventcontract.MatchReasonWouldDeliver:
			out.MatchedCount++
			if row.originalRecipient == "captured" {
				out.AlreadyCapturedCount++
			}
			out.Matches = append(out.Matches, api.EventReplayPreviewMatch{EventID: envelope.ID, EventSource: envelope.Source, EventType: envelope.Type, SchemaVersion: envelope.SchemaVersion, AcceptedAt: row.acceptedAt, OriginalRecipient: row.originalRecipient})
		case eventcontract.MatchReasonFilterMismatch:
			out.FilterMismatchCount++
		case eventcontract.MatchReasonPatternMismatch:
			out.PatternMismatchCount++
		default:
			return out, errors.New("retained event tenant mismatch")
		}
	}
	if hasMore {
		last := candidates[len(candidates)-1]
		c.AcceptedAt, c.OutboxID = last.acceptedAt, last.id
		data, err := json.Marshal(c)
		if err != nil {
			return out, fmt.Errorf("encode replay preview cursor: %w", err)
		}
		out.NextAfter = eventReplayPreviewCursorPrefix + base64.RawURLEncoding.EncodeToString(data)
	}
	return out, ctx.Err()
}

func (s *PgStore) PreviewEventReplay(ctx context.Context, accountID string, query EventReplayPreviewQuery) (api.EventReplayPreviewResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventReplayPreviewReadTimeout)
	defer cancel()
	if err := validateEventReplayPreviewQuery(accountID, &query); err != nil {
		return api.EventReplayPreviewResponse{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.EventReplayPreviewResponse{}, fmt.Errorf("begin retained event preview: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.EventReplayPreviewReadTimeout)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := sqlc.New()
	row, err := q.EventReplayPreviewTarget(ctx, tx, sqlc.EventReplayPreviewTargetParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(query.AppID), SubscriptionID: mustPgUUID(query.SubscriptionID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.EventReplayPreviewResponse{}, ErrNotFound
	}
	if err != nil {
		return api.EventReplayPreviewResponse{}, fmt.Errorf("read replay preview target: %w", err)
	}
	sub := EventSubscription{ID: uuidFromPgtype(row.ID).String(), AccountID: uuidFromPgtype(row.AccountID).String(), AppID: uuidFromPgtype(row.AppID).String(), Source: row.Source, Type: row.Type, Filter: row.Filter, Enabled: row.Enabled, CreatedAt: timeFromPgtype(row.CreatedAt), UpdatedAt: timeFromPgtype(row.UpdatedAt)}
	now := time.Now().UTC()
	c, err := eventReplayPreviewWindow(accountID, query, sub, row.WorkBound, now)
	if err != nil {
		return api.EventReplayPreviewResponse{}, err
	}
	first, err := q.EventReplayPreviewEarliestRetained(ctx, tx, mustPgUUID(accountID))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return api.EventReplayPreviewResponse{}, fmt.Errorf("read earliest retained event: %w", err)
	}
	var earliest *time.Time
	if first.Valid {
		t := timeFromPgtype(first)
		earliest = &t
	}
	afterAt := c.AcceptedAt
	if afterAt.IsZero() {
		afterAt = query.From
	}
	rows, err := q.EventReplayPreviewCandidates(ctx, tx, sqlc.EventReplayPreviewCandidatesParams{AccountID: mustPgUUID(accountID), AppID: query.AppID, SubscriptionID: query.SubscriptionID, FromAt: eventReplayPreviewPgBoundary(query.From), CutoffAt: eventReplayPreviewPgBoundary(c.CutoffAt), AfterAt: pgtype.Timestamptz{Time: afterAt, Valid: true}, AfterID: c.OutboxID, PageLimit: int32(query.Limit + 1)})
	if err != nil {
		return api.EventReplayPreviewResponse{}, fmt.Errorf("read retained event candidates: %w", err)
	}
	candidates := make([]retainedEventCandidate, len(rows))
	for i, r := range rows {
		candidates[i] = retainedEventCandidate{r.ID, timeFromPgtype(r.CreatedAt), r.Payload, r.OriginalRecipient}
	}
	out, err := evaluateEventReplayPreview(ctx, newEventReplayPreview(sub, row.AppSlug, c, now, earliest), sub, c, candidates, query.Limit)
	if err != nil {
		return out, err
	}
	if err := tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("commit retained event preview read: %w", err)
	}
	return out, nil
}

func (m *MemStore) PreviewEventReplay(ctx context.Context, accountID string, query EventReplayPreviewQuery) (api.EventReplayPreviewResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventReplayPreviewReadTimeout)
	defer cancel()
	if err := validateEventReplayPreviewQuery(accountID, &query); err != nil {
		return api.EventReplayPreviewResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.eventSubscriptionAppLocked(query.AppID)
	if !ok || app.Status == AppDeleted || !sameMemUUID(app.AccountID, accountID) {
		return api.EventReplayPreviewResponse{}, ErrNotFound
	}
	var sub EventSubscription
	for _, s := range m.eventSubscriptions {
		if s.ID == query.SubscriptionID && s.AppID == query.AppID && sameMemUUID(s.AccountID, accountID) {
			sub = s
			break
		}
	}
	if sub.ID == "" {
		return api.EventReplayPreviewResponse{}, ErrNotFound
	}
	_, bound := m.eventWorkBindings[sub.ID]
	now := time.Now().UTC()
	c, err := eventReplayPreviewWindow(accountID, query, sub, bound, now)
	if err != nil {
		return api.EventReplayPreviewResponse{}, err
	}
	candidates := make([]retainedEventCandidate, 0, query.Limit+1)
	var earliest *time.Time
	for key, work := range m.eventFanout {
		if err := ctx.Err(); err != nil {
			return api.EventReplayPreviewResponse{}, err
		}
		if !strings.HasPrefix(key, canonicalMemUUID(accountID)+"\x00") {
			continue
		}
		if earliest == nil || work.CreatedAt.Before(*earliest) {
			t := work.CreatedAt
			earliest = &t
		}
		if work.CreatedAt.Before(query.From) || !work.CreatedAt.Before(c.CutoffAt) || (!c.AcceptedAt.IsZero() && (work.CreatedAt.Before(c.AcceptedAt) || work.CreatedAt.Equal(c.AcceptedAt) && work.ID <= c.OutboxID)) {
			continue
		}
		original := "unknown"
		if work.SnapshotCaptured {
			original = "not_captured"
			for _, r := range work.RecipientSnapshot {
				if r.ID == sub.ID && sameMemUUID(r.AppID, sub.AppID) {
					original = "captured"
					break
				}
			}
		}
		candidate := retainedEventCandidate{work.ID, work.CreatedAt, work.Payload, original}
		// Keep only the bounded earliest page, even in this in-memory adapter.
		i := sort.Search(len(candidates), func(i int) bool {
			return candidates[i].acceptedAt.After(candidate.acceptedAt) || candidates[i].acceptedAt.Equal(candidate.acceptedAt) && candidates[i].id > candidate.id
		})
		if i > query.Limit {
			continue
		}
		candidates = append(candidates, retainedEventCandidate{})
		copy(candidates[i+1:], candidates[i:])
		candidates[i] = candidate
		if len(candidates) > query.Limit+1 {
			candidates = candidates[:query.Limit+1]
		}
	}
	return evaluateEventReplayPreview(ctx, newEventReplayPreview(sub, app.Slug, c, now, earliest), sub, c, candidates, query.Limit)
}
