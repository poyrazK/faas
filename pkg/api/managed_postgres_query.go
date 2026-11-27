package api

import (
	"context"
	"net/http"
	"net/url"
)

// ManagedPostgresQueryRequest is one read-only statement
// (ADR-20261117215858 §2). MaxRows defaults to 100, at most 1000.
type ManagedPostgresQueryRequest struct {
	SQL     string `json:"sql"`
	MaxRows int    `json:"max_rows,omitempty"`
}

// ManagedPostgresQueryColumn is one result column.
type ManagedPostgresQueryColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// ManagedPostgresQueryResponse holds the rows read, each value in its
// PostgreSQL text form or null. Truncated is true when the row or 1 MiB cap
// stopped reading before the result ended.
type ManagedPostgresQueryResponse struct {
	DatabaseID string                       `json:"database_id"`
	Columns    []ManagedPostgresQueryColumn `json:"columns"`
	Rows       [][]*string                  `json:"rows"`
	RowCount   int                          `json:"row_count"`
	Truncated  bool                         `json:"truncated"`
	DurationMS int64                        `json:"duration_ms"`
}

// CodePostgresQueryFailed is PostgreSQL refusing or failing the statement;
// the problem detail carries its message and SQLSTATE.
const (
	CodePostgresQueryFailed      = "postgres_query_failed"
	CodePostgresQueryUnsupported = "postgres_query_unsupported"
)

// QueryManagedPostgresDatabase runs one read-only statement as the
// database's agent query login. It needs the data:read scope.
func (c *Client) QueryManagedPostgresDatabase(ctx context.Context, id string, req ManagedPostgresQueryRequest) (ManagedPostgresQueryResponse, error) {
	var out ManagedPostgresQueryResponse
	err := c.do(ctx, http.MethodPost, "/v1/postgres/databases/"+url.PathEscape(id)+"/query", req, &out)
	return out, err
}
