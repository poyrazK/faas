package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

type SetManagedPostgresBackupScheduleRequest struct {
	DailyAtUTC string `json:"daily_at_utc"`
	BucketApp  string `json:"bucket_app"`
	BucketID   string `json:"bucket_id"`
}

type ManagedPostgresBackupSchedule struct {
	DatabaseID    string     `json:"database_id"`
	BucketAppID   string     `json:"bucket_app_id"`
	BucketID      string     `json:"bucket_id"`
	DailyAtUTC    string     `json:"daily_at_utc"`
	NextRunAt     time.Time  `json:"next_run_at"`
	State         string     `json:"state"`
	ActiveRunID   string     `json:"active_run_id,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastArchive   string     `json:"last_archive_key,omitempty"`
	LastError     string     `json:"last_error_code,omitempty"`
}

func managedPostgresBackupSchedulePath(org, database string) string {
	prefix := "/v1/postgres"
	if org != "" {
		prefix = orgPostgresPath(org)
	}
	return prefix + "/databases/" + url.PathEscape(database) + "/backup-schedule"
}

func (c *PreviewClient) GetManagedPostgresBackupSchedule(ctx context.Context, org, database string) (ManagedPostgresBackupSchedule, error) {
	var out ManagedPostgresBackupSchedule
	err := c.do(ctx, http.MethodGet, managedPostgresBackupSchedulePath(org, database), nil, &out)
	return out, err
}

func (c *PreviewClient) SetManagedPostgresBackupSchedule(ctx context.Context, org, database string, request SetManagedPostgresBackupScheduleRequest) (ManagedPostgresBackupSchedule, error) {
	var out ManagedPostgresBackupSchedule
	err := c.do(ctx, http.MethodPut, managedPostgresBackupSchedulePath(org, database), request, &out)
	return out, err
}

func (c *PreviewClient) DeleteManagedPostgresBackupSchedule(ctx context.Context, org, database string) error {
	return c.do(ctx, http.MethodDelete, managedPostgresBackupSchedulePath(org, database), nil, nil)
}
