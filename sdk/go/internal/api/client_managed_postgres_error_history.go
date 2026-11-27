package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type ManagedPostgresQueryError struct {
	ObservedAt time.Time `json:"observed_at"`
	Severity   string    `json:"severity"`
	SQLState   string    `json:"sqlstate"`
	BindingID  string    `json:"binding_id,omitempty"`
}

type ManagedPostgresErrorHistory struct {
	DatabaseID string                      `json:"database_id"`
	Enabled    bool                        `json:"enabled"`
	Since      time.Time                   `json:"since"`
	Events     []ManagedPostgresQueryError `json:"events"`
	HasMore    bool                        `json:"has_more"`
}

type SetManagedPostgresErrorHistoryRequest struct {
	Enabled bool `json:"enabled"`
}

func managedPostgresErrorHistoryPath(org, database string) string {
	prefix := "/v1/postgres"
	if org != "" {
		prefix = orgPostgresPath(org)
	}
	return prefix + "/databases/" + url.PathEscape(database) + "/errors"
}

func (c *PreviewClient) GetManagedPostgresErrorHistory(ctx context.Context, org, database string, since time.Duration, sqlstate string, limit int) (ManagedPostgresErrorHistory, error) {
	query := url.Values{"since_seconds": {strconv.FormatInt(int64(since/time.Second), 10)}, "limit": {strconv.Itoa(limit)}}
	if sqlstate != "" {
		query.Set("sqlstate", sqlstate)
	}
	var out ManagedPostgresErrorHistory
	err := c.do(ctx, http.MethodGet, managedPostgresErrorHistoryPath(org, database)+"?"+query.Encode(), nil, &out)
	return out, err
}

func (c *PreviewClient) SetManagedPostgresErrorHistory(ctx context.Context, org, database string, request SetManagedPostgresErrorHistoryRequest) (ManagedPostgresErrorHistory, error) {
	var out ManagedPostgresErrorHistory
	err := c.do(ctx, http.MethodPut, managedPostgresErrorHistoryPath(org, database), request, &out)
	return out, err
}
