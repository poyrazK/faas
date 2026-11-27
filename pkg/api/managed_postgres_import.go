package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// ManagedPostgresImportSource identifies a PostgreSQL login used for an import.
type ManagedPostgresImportSource struct {
	Host           string   `json:"host"`
	Port           int      `json:"port"`
	Database       string   `json:"database"`
	User           string   `json:"user"`
	Password       string   `json:"password"`
	SkipExtensions []string `json:"skip_extensions,omitempty"`
}

func (ManagedPostgresImportSource) String() string { return "[REDACTED]" }
func (ManagedPostgresImportSource) GoString() string {
	return "ManagedPostgresImportSource{[REDACTED]}"
}

type ManagedPostgresImportRequest struct {
	OperationID string                      `json:"operation_id"`
	Source      ManagedPostgresImportSource `json:"source"`
}

type ManagedPostgresImportStatus struct {
	OperationID            string                            `json:"operation_id,omitempty"`
	State                  string                            `json:"state"`
	ErrorCode              string                            `json:"error_code,omitempty"`
	SourceMajor            int                               `json:"source_major,omitempty"`
	StartedAt              time.Time                         `json:"started_at,omitzero"`
	FinishedAt             time.Time                         `json:"finished_at,omitzero"`
	MissingExtensions      []string                          `json:"missing_extensions,omitempty"`
	IncompatibleExtensions []ManagedPostgresExtensionVersion `json:"incompatible_extensions,omitempty"`
	RequiredVolumeBytes    int64                             `json:"required_volume_bytes,omitempty"`
}

func (c *Client) CheckManagedPostgresImport(ctx context.Context, id string, source ManagedPostgresImportSource) (ManagedPostgresImportStatus, error) {
	return c.managedPostgresImport(ctx, "/v1/postgres", id, http.MethodPost, "/check", source)
}

func (c *Client) StartManagedPostgresImport(ctx context.Context, id string, request ManagedPostgresImportRequest) (ManagedPostgresImportStatus, error) {
	return c.managedPostgresImport(ctx, "/v1/postgres", id, http.MethodPost, "", request)
}

func (c *Client) GetManagedPostgresImport(ctx context.Context, id, operation string) (ManagedPostgresImportStatus, error) {
	return c.managedPostgresImport(ctx, "/v1/postgres", id, http.MethodGet, "/"+url.PathEscape(operation), nil)
}

func (c *Client) CheckOrgManagedPostgresImport(ctx context.Context, organization, id string, source ManagedPostgresImportSource) (ManagedPostgresImportStatus, error) {
	return c.managedPostgresImport(ctx, orgPostgresPath(organization), id, http.MethodPost, "/check", source)
}

func (c *Client) StartOrgManagedPostgresImport(ctx context.Context, organization, id string, request ManagedPostgresImportRequest) (ManagedPostgresImportStatus, error) {
	return c.managedPostgresImport(ctx, orgPostgresPath(organization), id, http.MethodPost, "", request)
}

func (c *Client) GetOrgManagedPostgresImport(ctx context.Context, organization, id, operation string) (ManagedPostgresImportStatus, error) {
	return c.managedPostgresImport(ctx, orgPostgresPath(organization), id, http.MethodGet, "/"+url.PathEscape(operation), nil)
}

func (c *Client) managedPostgresImport(ctx context.Context, prefix, id, method, tail string, body any) (ManagedPostgresImportStatus, error) {
	var result ManagedPostgresImportStatus
	err := c.do(ctx, method, prefix+"/databases/"+url.PathEscape(id)+"/imports"+tail, body, &result)
	return result, err
}
