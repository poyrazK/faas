package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// ManagedPostgresRestoreCheck is the archive evidence for a restore target.
type ManagedPostgresRestoreCheck struct {
	PointInTime            time.Time  `json:"point_in_time"`
	Outcome                string     `json:"outcome"`
	Reason                 string     `json:"reason"`
	BackupCompletedAt      *time.Time `json:"backup_completed_at,omitempty"`
	WALStopAt              *time.Time `json:"wal_stop_at,omitempty"`
	MissingWALSegment      string     `json:"missing_wal_segment,omitempty"`
	RecoveryTimeline       uint32     `json:"recovery_timeline,omitempty"`
	MissingTimelineHistory string     `json:"missing_timeline_history,omitempty"`
}

// CheckManagedPostgresRestore reads native archive evidence for a target.
func (c *Client) CheckManagedPostgresRestore(ctx context.Context, id string, point time.Time) (ManagedPostgresRestoreCheck, error) {
	return c.checkManagedPostgresRestore(ctx, "/v1/postgres", id, point)
}

// CheckOrgManagedPostgresRestore reads native archive evidence for an organization database.
func (c *Client) CheckOrgManagedPostgresRestore(ctx context.Context, organization, id string, point time.Time) (ManagedPostgresRestoreCheck, error) {
	return c.checkManagedPostgresRestore(ctx, orgPostgresPath(organization), id, point)
}

func (c *Client) checkManagedPostgresRestore(ctx context.Context, prefix, id string, point time.Time) (ManagedPostgresRestoreCheck, error) {
	path := prefix + "/databases/" + url.PathEscape(id) + "/restore-check?" +
		url.Values{"point_in_time": {point.UTC().Format(time.RFC3339Nano)}}.Encode()
	var out ManagedPostgresRestoreCheck
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
