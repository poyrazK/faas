package api

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// DeletedAppSummary is account-owned lifecycle evidence. Restore eligibility
// does not reserve quota/capacity or prove the app will execute after restore.
type DeletedAppSummary struct {
	AccountID        string     `json:"account_id"`
	ID               string     `json:"id"`
	Slug             string     `json:"slug"`
	DeletedAt        *time.Time `json:"deleted_at"`
	DeleteGraceUntil *time.Time `json:"delete_grace_until"`
	RestoreEligible  bool       `json:"restore_eligible"`
	RestoreBlocker   string     `json:"restore_blocker"`
	EvaluatedAt      time.Time  `json:"evaluated_at"`
}

// DeletedAppListResponse preserves the opaque account-bound cursor.
type DeletedAppListResponse struct {
	AccountID   string              `json:"account_id"`
	Apps        []DeletedAppSummary `json:"apps"`
	NextCursor  string              `json:"next_cursor,omitempty"`
	EvaluatedAt time.Time           `json:"evaluated_at"`
}

// ListDeletedApps reads at most 50 account-owned tombstones. A zero limit uses
// the server default. Never share a cursor across accounts.
func (c *Client) ListDeletedApps(ctx context.Context, cursor string, limit int) (DeletedAppListResponse, error) {
	query := url.Values{}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if limit != 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/deleted-apps"
	if len(query) != 0 {
		path += "?" + query.Encode()
	}
	var out DeletedAppListResponse
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetDeletedApp refreshes one exact retained identity before a restore review.
func (c *Client) GetDeletedApp(ctx context.Context, id string) (DeletedAppSummary, error) {
	var out DeletedAppSummary
	return out, c.do(ctx, "GET", "/v1/deleted-apps/"+url.PathEscape(id), nil, &out)
}

// DeletedAppPurgeResponse reports POST /v1/deleted-apps/{id}/purge. The
// restore window is closed either way. Purged is true once the app and its
// stored artifacts are gone and the slug is free for this account; otherwise
// PendingReason says what still holds it and the grace sweep finishes the
// purge on its own.
type DeletedAppPurgeResponse struct {
	AccountID     string `json:"account_id"`
	ID            string `json:"id"`
	Slug          string `json:"slug"`
	Purged        bool   `json:"purged"`
	PendingReason string `json:"pending_reason,omitempty"`
}

// PurgeDeletedApp permanently removes one deleted app now instead of at the
// end of its restore window, freeing its slug for the same account. It cannot
// be undone.
func (c *Client) PurgeDeletedApp(ctx context.Context, id string) (DeletedAppPurgeResponse, error) {
	var out DeletedAppPurgeResponse
	return out, c.doWithoutIdempotencyKey(ctx, "POST", "/v1/deleted-apps/"+url.PathEscape(id)+"/purge", nil, &out)
}

// RestoreDeletedApp uses the dedicated ID route, with no legacy slug fallback.
// The server does not offer idempotent replay. After an uncertain response,
// reconcile exact-ID inventory/app reads instead of automatically retrying.
func (c *Client) RestoreDeletedApp(ctx context.Context, id string) (AppResponse, error) {
	var out AppResponse
	return out, c.doWithoutIdempotencyKey(ctx, "POST", "/v1/deleted-apps/"+url.PathEscape(id)+"/restore", nil, &out)
}
