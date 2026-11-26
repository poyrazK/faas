package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// ManagedPostgresMigrationCheck reports evidence for a logical migration.
type ManagedPostgresMigrationCheck struct {
	Outcome               string   `json:"outcome"`
	Reasons               []string `json:"reasons,omitempty"`
	SourceMajor           int      `json:"source_major,omitempty"`
	TargetMajor           int      `json:"target_major,omitempty"`
	TableCount            int64    `json:"table_count"`
	SequenceCount         int64    `json:"sequence_count"`
	UnsupportedTableCount int64    `json:"unsupported_table_count,omitempty"`
	UnsupportedTables     []string `json:"unsupported_tables,omitempty"`
	SourceBytes           int64    `json:"source_bytes,omitempty"`
	TargetFreeBytes       int64    `json:"target_free_bytes,omitempty"`
	SourceWALLimitBytes   int64    `json:"source_wal_limit_bytes"`
	FreeSourceSlots       int64    `json:"free_source_slots"`
	FreeSourceSenders     int64    `json:"free_source_senders"`
}

func (c *Client) CheckManagedPostgresMigration(ctx context.Context, id string, source ManagedPostgresImportSource) (ManagedPostgresMigrationCheck, error) {
	return c.checkManagedPostgresMigration(ctx, "/v1/postgres", id, source)
}

func (c *Client) CheckOrgManagedPostgresMigration(ctx context.Context, organization, id string, source ManagedPostgresImportSource) (ManagedPostgresMigrationCheck, error) {
	return c.checkManagedPostgresMigration(ctx, orgPostgresPath(organization), id, source)
}

func (c *Client) checkManagedPostgresMigration(ctx context.Context, prefix, id string, source ManagedPostgresImportSource) (ManagedPostgresMigrationCheck, error) {
	var result ManagedPostgresMigrationCheck
	err := c.do(ctx, http.MethodPost, prefix+"/databases/"+url.PathEscape(id)+"/migration-check", source, &result)
	return result, err
}

// ManagedPostgresMigrationProgress describes the latest verified replication sample.
type ManagedPostgresMigrationProgress struct {
	Verification     *ManagedPostgresMigrationVerification `json:"verification,omitempty"`
	Preparation      *ManagedPostgresMigrationPreparation  `json:"preparation,omitempty"`
	OperationID      string                                `json:"operation_id,omitempty"`
	State            string                                `json:"state"`
	Reason           string                                `json:"reason,omitempty"`
	Tables           int                                   `json:"tables"`
	TablesReady      int                                   `json:"tables_ready"`
	ApplyLagBytes    int64                                 `json:"apply_lag_bytes"`
	RetainedWALBytes int64                                 `json:"retained_wal_bytes"`
}

func (c *Client) StartManagedPostgresMigration(ctx context.Context, id string, request ManagedPostgresImportRequest) (ManagedPostgresMigrationProgress, error) {
	return c.managedPostgresMigration(ctx, "/v1/postgres", id, http.MethodPost, "", &request)
}

func (c *Client) StartOrgManagedPostgresMigration(ctx context.Context, org, id string, request ManagedPostgresImportRequest) (ManagedPostgresMigrationProgress, error) {
	return c.managedPostgresMigration(ctx, orgPostgresPath(org), id, http.MethodPost, "", &request)
}

// ManagedPostgresMigrationPreparation reports schema work before the final source write pause.
type ManagedPostgresMigrationPreparation struct {
	State        string                                       `json:"state"`
	Reason       string                                       `json:"reason,omitempty"`
	IndexesTotal int                                          `json:"indexes_total"`
	IndexesReady int                                          `json:"indexes_ready"`
	Sequences    int64                                        `json:"sequences"`
	Blockers     []ManagedPostgresMigrationPreparationBlocker `json:"blockers,omitempty"`
}

type ManagedPostgresMigrationPreparationBlocker struct {
	Code  string `json:"code"`
	Count int64  `json:"count"`
}

func (c *Client) PrepareManagedPostgresMigration(ctx context.Context, id string, request ManagedPostgresImportRequest) (ManagedPostgresMigrationProgress, error) {
	return c.managedPostgresMigration(ctx, "/v1/postgres", id, http.MethodPost, "/prepare", &request)
}

func (c *Client) PrepareOrgManagedPostgresMigration(ctx context.Context, org, id string, request ManagedPostgresImportRequest) (ManagedPostgresMigrationProgress, error) {
	return c.managedPostgresMigration(ctx, orgPostgresPath(org), id, http.MethodPost, "/prepare", &request)
}

type ManagedPostgresMigrationVerifyRequest struct {
	ManagedPostgresImportRequest
	VerificationID     string `json:"verification_id"`
	SourceWritesPaused bool   `json:"source_writes_paused"`
}

// ManagedPostgresMigrationVerification reports replay evidence for a declared write pause.
type ManagedPostgresMigrationVerification struct {
	ID              string    `json:"verification_id"`
	State           string    `json:"state"`
	Reason          string    `json:"reason,omitempty"`
	AppliedLSN      string    `json:"applied_lsn,omitempty"`
	SequencesSynced int       `json:"sequences_synced"`
	VerifiedAt      time.Time `json:"verified_at,omitzero"`
}

func (c *Client) VerifyManagedPostgresMigration(ctx context.Context, id string, request ManagedPostgresMigrationVerifyRequest) (ManagedPostgresMigrationProgress, error) {
	return c.managedPostgresMigration(ctx, "/v1/postgres", id, http.MethodPost, "/verify", request)
}

func (c *Client) VerifyOrgManagedPostgresMigration(ctx context.Context, org, id string, request ManagedPostgresMigrationVerifyRequest) (ManagedPostgresMigrationProgress, error) {
	return c.managedPostgresMigration(ctx, orgPostgresPath(org), id, http.MethodPost, "/verify", request)
}

func (c *Client) StopManagedPostgresMigration(ctx context.Context, id string, request ManagedPostgresImportRequest) (ManagedPostgresMigrationProgress, error) {
	return c.managedPostgresMigration(ctx, "/v1/postgres", id, http.MethodPost, "/stop", &request)
}

func (c *Client) StopOrgManagedPostgresMigration(ctx context.Context, org, id string, request ManagedPostgresImportRequest) (ManagedPostgresMigrationProgress, error) {
	return c.managedPostgresMigration(ctx, orgPostgresPath(org), id, http.MethodPost, "/stop", &request)
}

func (c *Client) GetManagedPostgresMigration(ctx context.Context, id string) (ManagedPostgresMigrationProgress, error) {
	return c.managedPostgresMigration(ctx, "/v1/postgres", id, http.MethodGet, "", nil)
}

func (c *Client) GetOrgManagedPostgresMigration(ctx context.Context, org, id string) (ManagedPostgresMigrationProgress, error) {
	return c.managedPostgresMigration(ctx, orgPostgresPath(org), id, http.MethodGet, "", nil)
}

func (c *Client) managedPostgresMigration(ctx context.Context, prefix, id, method, suffix string, body any) (ManagedPostgresMigrationProgress, error) {
	var result ManagedPostgresMigrationProgress
	err := c.do(ctx, method, prefix+"/databases/"+url.PathEscape(id)+"/migration"+suffix, body, &result)
	return result, err
}
