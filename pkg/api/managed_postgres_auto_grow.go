package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// SetManagedPostgresAutoGrowRequest sets a per-volume ceiling in bytes. Zero
// disables future automatic growth.
type SetManagedPostgresAutoGrowRequest struct {
	MaxBytes int64 `json:"max_bytes"`
}

type ManagedPostgresAutoGrowStatus struct {
	MaxBytes      int64      `json:"max_bytes"`
	ProposedBytes int64      `json:"proposed_bytes,omitempty"`
	Reason        string     `json:"reason"`
	ResizeID      string     `json:"resize_id,omitempty"`
	LastGrownAt   *time.Time `json:"last_grown_at,omitempty"`
}

func (c *Client) SetManagedPostgresAutoGrow(ctx context.Context, id string, req SetManagedPostgresAutoGrowRequest) (ManagedPostgresAutoGrowStatus, error) {
	var result ManagedPostgresAutoGrowStatus
	err := c.do(ctx, http.MethodPut, "/v1/postgres/databases/"+url.PathEscape(id)+"/auto-grow", req, &result)
	return result, err
}
