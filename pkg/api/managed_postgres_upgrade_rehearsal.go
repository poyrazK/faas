package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

type RequestManagedPostgresUpgradeRehearsal struct {
	RequestID     string `json:"request_id"`
	PostgresMajor int    `json:"postgres_major"`
}

type ManagedPostgresUpgradeRehearsal struct {
	DatabaseID      string     `json:"database_id"`
	RehearsalID     string     `json:"rehearsal_id"`
	RequestID       string     `json:"request_id"`
	SourceMajor     int        `json:"source_major"`
	TargetMajor     int        `json:"target_major"`
	Outcome         string     `json:"outcome"`
	Phase           string     `json:"phase,omitempty"`
	FailedPhase     string     `json:"failed_phase,omitempty"`
	RecoveryPoint   *time.Time `json:"recovery_point,omitempty"`
	RecoveredTo     *time.Time `json:"recovered_to,omitempty"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	DurationSeconds *int64     `json:"duration_seconds,omitempty"`
	ErrorCode       string     `json:"error_code,omitempty"`
}

func (c *Client) StartManagedPostgresUpgradeRehearsal(ctx context.Context, id string, req RequestManagedPostgresUpgradeRehearsal) (ManagedPostgresUpgradeRehearsal, error) {
	return c.startManagedPostgresUpgradeRehearsal(ctx, "/v1/postgres", id, req)
}

func (c *Client) StartOrgManagedPostgresUpgradeRehearsal(ctx context.Context, organization, id string, req RequestManagedPostgresUpgradeRehearsal) (ManagedPostgresUpgradeRehearsal, error) {
	return c.startManagedPostgresUpgradeRehearsal(ctx, orgPostgresPath(organization), id, req)
}

func (c *Client) startManagedPostgresUpgradeRehearsal(ctx context.Context, prefix, id string, req RequestManagedPostgresUpgradeRehearsal) (ManagedPostgresUpgradeRehearsal, error) {
	var out ManagedPostgresUpgradeRehearsal
	err := c.do(ctx, http.MethodPost, prefix+"/databases/"+url.PathEscape(id)+"/upgrade-rehearsals", req, &out)
	return out, err
}

func (c *Client) GetManagedPostgresUpgradeRehearsal(ctx context.Context, id, rehearsalID string) (ManagedPostgresUpgradeRehearsal, error) {
	return c.getManagedPostgresUpgradeRehearsal(ctx, "/v1/postgres", id, rehearsalID)
}

func (c *Client) GetOrgManagedPostgresUpgradeRehearsal(ctx context.Context, organization, id, rehearsalID string) (ManagedPostgresUpgradeRehearsal, error) {
	return c.getManagedPostgresUpgradeRehearsal(ctx, orgPostgresPath(organization), id, rehearsalID)
}

func (c *Client) getManagedPostgresUpgradeRehearsal(ctx context.Context, prefix, id, rehearsalID string) (ManagedPostgresUpgradeRehearsal, error) {
	var out ManagedPostgresUpgradeRehearsal
	err := c.do(ctx, http.MethodGet, prefix+"/databases/"+url.PathEscape(id)+"/upgrade-rehearsals/"+url.PathEscape(rehearsalID), nil, &out)
	return out, err
}
