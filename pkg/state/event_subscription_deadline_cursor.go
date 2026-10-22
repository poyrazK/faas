package state

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type subscriptionDeadlineCursor struct {
	Version    int       `json:"v"`
	Scope      string    `json:"scope"`
	WindowAt   time.Time `json:"window_at"`
	DeadlineAt time.Time `json:"deadline_at"`
	AcceptedAt time.Time `json:"accepted_at"`
	OutboxID   int64     `json:"outbox_id,string"`
}
type subscriptionDeadlineEntry struct {
	api.EventSubscriptionDeadlineRecipient
	outboxID int64
}

func encodeSubscriptionDeadlineCursor(c subscriptionDeadlineCursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func subscriptionDeadlineTimeValid(at time.Time) bool {
	return !at.IsZero() && at.Year() >= 1 && at.Year() <= 9999 && at.Location() == time.UTC && at.Equal(at.Truncate(time.Microsecond))
}
func subscriptionDeadlineQuery(account, app, sub string, q api.EventSubscriptionDeadlineQuery, now time.Time) (api.EventSubscriptionDeadlineQuery, subscriptionDeadlineCursor, error) {
	c := subscriptionDeadlineCursor{Version: 1, WindowAt: now.UTC().Truncate(time.Microsecond)}
	if validSubscriptionControlIDs(account, app, sub) != nil || q.Validate() != nil {
		return q, c, ErrInvalidArgument
	}
	if q.WithinMillis > 0 {
		q.Status = "upcoming"
	}
	if q.Status == "" {
		q.Status = "all"
	}
	if q.Limit == 0 {
		q.Limit = api.EventSubscriptionDeadlinePageDefault
	}
	var err error
	q.DeadlineFrom, err = normalizeAcknowledgmentHistoryTime(q.DeadlineFrom)
	if err != nil {
		return q, c, ErrInvalidArgument
	}
	q.DeadlineBefore, err = normalizeAcknowledgmentHistoryTime(q.DeadlineBefore)
	if err != nil || q.Validate() != nil {
		return q, c, ErrInvalidArgument
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
	var saved subscriptionDeadlineCursor
	if err != nil || json.Unmarshal(raw, &saved) != nil || encodeSubscriptionDeadlineCursor(saved) != token || saved.Version != 1 || saved.Scope != c.Scope || !subscriptionDeadlineTimeValid(saved.WindowAt) || saved.WindowAt.After(c.WindowAt) || !saved.WindowAt.Add(api.EventSubscriptionDeadlineCursorLifetime).After(c.WindowAt) || !subscriptionDeadlineTimeValid(saved.DeadlineAt) || !subscriptionDeadlineTimeValid(saved.AcceptedAt) || saved.AcceptedAt.After(saved.WindowAt) || !saved.DeadlineAt.After(saved.AcceptedAt) || saved.OutboxID < 1 {
		return q, c, ErrInvalidArgument
	}
	if !subscriptionDeadlineMatches(saved.DeadlineAt, q, saved.WindowAt) {
		return q, c, ErrInvalidArgument
	}
	return q, saved, nil
}
func subscriptionDeadlineMatches(deadline time.Time, q api.EventSubscriptionDeadlineQuery, window time.Time) bool {
	return !deadline.IsZero() && (q.WithinMillis == 0 || deadline.Before(window.Add(time.Duration(q.WithinMillis)*time.Millisecond))) && (q.DeadlineFrom == nil || !deadline.Before(*q.DeadlineFrom)) && (q.DeadlineBefore == nil || deadline.Before(*q.DeadlineBefore)) && (q.Status != "expired" || !deadline.After(window)) && (q.Status != "upcoming" || deadline.After(window))
}
func subscriptionDeadlineEntryBefore(a, b subscriptionDeadlineEntry) bool {
	if !a.DeadlineAt.Equal(b.DeadlineAt) {
		return a.DeadlineAt.Before(b.DeadlineAt)
	}
	if !a.AcceptedAt.Equal(b.AcceptedAt) {
		return a.AcceptedAt.Before(b.AcceptedAt)
	}
	return a.outboxID < b.outboxID
}
func subscriptionDeadlinePage(app, sub string, now time.Time, q api.EventSubscriptionDeadlineQuery, c subscriptionDeadlineCursor, rows []subscriptionDeadlineEntry) api.EventSubscriptionDeadlinePage {
	out := api.EventSubscriptionDeadlinePage{Coverage: "retained_captured_pending_application_deadlines", AppID: canonicalMemUUID(app), SubscriptionID: canonicalMemUUID(sub), ObservedAt: now, WindowAt: c.WindowAt, ReadOnly: true, Recipients: []api.EventSubscriptionDeadlineRecipient{}, HasMore: len(rows) > q.Limit}
	if out.HasMore {
		rows = rows[:q.Limit]
	}
	for _, row := range rows {
		row.Expired = !row.DeadlineAt.After(now)
		out.Recipients = append(out.Recipients, row.EventSubscriptionDeadlineRecipient)
	}
	if out.HasMore {
		last := rows[len(rows)-1]
		c.DeadlineAt = last.DeadlineAt
		c.AcceptedAt = last.AcceptedAt
		c.OutboxID = last.outboxID
		out.NextCursor = encodeSubscriptionDeadlineCursor(c)
	}
	return out
}
