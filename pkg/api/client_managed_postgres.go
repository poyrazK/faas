package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

func (c *Client) ListManagedPostgresAccountingDiagnostics(ctx context.Context, accountID, afterID string, limit int) (ManagedPostgresAccountingDiagnosticsResponse, error) {
	var out ManagedPostgresAccountingDiagnosticsResponse
	query := url.Values{}
	if afterID != "" {
		query.Set("after", afterID)
	}
	if limit != 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/admin/managed-postgres/accounting/" + url.PathEscape(accountID)
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) PreviewManagedPostgresUsageImport(ctx context.Context, accountID string, request ManagedPostgresUsageImportRequest) (ManagedPostgresUsageImportResult, error) {
	var out ManagedPostgresUsageImportResult
	err := c.do(ctx, http.MethodPost, "/v1/admin/managed-postgres/accounting/"+url.PathEscape(accountID)+"/usage-imports/preview", request, &out)
	return out, err
}

func (c *Client) ApplyManagedPostgresUsageImport(ctx context.Context, accountID string, request ManagedPostgresUsageImportRequest) (ManagedPostgresUsageImportResult, error) {
	var out ManagedPostgresUsageImportResult
	err := c.do(ctx, http.MethodPost, "/v1/admin/managed-postgres/accounting/"+url.PathEscape(accountID)+"/usage-imports", request, &out)
	return out, err
}

func (c *Client) GetManagedPostgresUsage(ctx context.Context) (ManagedPostgresUsageResponse, error) {
	var out ManagedPostgresUsageResponse
	err := c.do(ctx, http.MethodGet, "/v1/account/managed-postgres-usage", nil, &out)
	return out, err
}

func (c *Client) PreviewManagedPostgresAccountingReconciliation(ctx context.Context, accountID string, request ManagedPostgresAccountingReconciliationRequest) (ManagedPostgresAccountingReconciliationResult, error) {
	var out ManagedPostgresAccountingReconciliationResult
	err := c.do(ctx, http.MethodPost, "/v1/admin/managed-postgres/accounting/"+url.PathEscape(accountID)+"/reconciliations/preview", request, &out)
	return out, err
}

func (c *Client) ApplyManagedPostgresAccountingReconciliation(ctx context.Context, accountID string, request ManagedPostgresAccountingReconciliationRequest) (ManagedPostgresAccountingReconciliationResult, error) {
	var out ManagedPostgresAccountingReconciliationResult
	err := c.do(ctx, http.MethodPost, "/v1/admin/managed-postgres/accounting/"+url.PathEscape(accountID)+"/reconciliations", request, &out)
	return out, err
}

func (c *Client) ListManagedPostgresDatabases(ctx context.Context) (ManagedPostgresDatabaseList, error) {
	var out ManagedPostgresDatabaseList
	err := c.do(ctx, http.MethodGet, "/v1/postgres/databases", nil, &out)
	return out, err
}

func (c *Client) CreateManagedPostgresDatabase(ctx context.Context, req CreateManagedPostgresDatabaseRequest) (ManagedPostgresDatabase, error) {
	var out ManagedPostgresDatabase
	err := c.do(ctx, http.MethodPost, "/v1/postgres/databases", req, &out)
	return out, err
}

func (c *Client) GetManagedPostgresDatabase(ctx context.Context, id string) (ManagedPostgresDatabase, error) {
	var out ManagedPostgresDatabase
	err := c.do(ctx, http.MethodGet, "/v1/postgres/databases/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) DeleteManagedPostgresDatabase(ctx context.Context, id string) (ManagedPostgresDatabase, error) {
	var out ManagedPostgresDatabase
	err := c.do(ctx, http.MethodDelete, "/v1/postgres/databases/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) RestoreManagedPostgresDatabase(ctx context.Context, id string, req RestoreManagedPostgresDatabaseRequest) (ManagedPostgresDatabase, error) {
	var out ManagedPostgresDatabase
	err := c.do(ctx, http.MethodPost, "/v1/postgres/databases/"+url.PathEscape(id)+"/restore", req, &out)
	return out, err
}

func (c *Client) ListManagedPostgresBindings(ctx context.Context, databaseID string) (ManagedPostgresBindingList, error) {
	var out ManagedPostgresBindingList
	err := c.do(ctx, http.MethodGet, "/v1/postgres/databases/"+url.PathEscape(databaseID)+"/bindings", nil, &out)
	return out, err
}

func (c *Client) CreateManagedPostgresBinding(ctx context.Context, databaseID string, req CreateManagedPostgresBindingRequest) (ManagedPostgresBinding, error) {
	var out ManagedPostgresBinding
	err := c.do(ctx, http.MethodPost, "/v1/postgres/databases/"+url.PathEscape(databaseID)+"/bindings", req, &out)
	return out, err
}

func (c *Client) GetManagedPostgresBinding(ctx context.Context, id string) (ManagedPostgresBinding, error) {
	var out ManagedPostgresBinding
	err := c.do(ctx, http.MethodGet, "/v1/postgres/bindings/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) DeleteManagedPostgresBinding(ctx context.Context, id string) (ManagedPostgresBinding, error) {
	var out ManagedPostgresBinding
	err := c.do(ctx, http.MethodDelete, "/v1/postgres/bindings/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) RotateManagedPostgresBinding(ctx context.Context, id string) (ManagedPostgresBinding, error) {
	var out ManagedPostgresBinding
	err := c.do(ctx, http.MethodPost, "/v1/postgres/bindings/"+url.PathEscape(id)+"/rotate", nil, &out)
	return out, err
}

func (c *Client) PrepareManagedPostgresCutover(ctx context.Context, req PrepareManagedPostgresCutoverRequest) (ManagedPostgresCutover, error) {
	var out ManagedPostgresCutover
	err := c.do(ctx, http.MethodPost, "/v1/postgres/cutovers", req, &out)
	return out, err
}
func (c *Client) GetManagedPostgresCutover(ctx context.Context, id string) (ManagedPostgresCutover, error) {
	var out ManagedPostgresCutover
	err := c.do(ctx, http.MethodGet, "/v1/postgres/cutovers/"+url.PathEscape(id), nil, &out)
	return out, err
}
func (c *Client) VerifyManagedPostgresCutover(ctx context.Context, id string) (ManagedPostgresCutover, error) {
	var out ManagedPostgresCutover
	err := c.do(ctx, http.MethodPost, "/v1/postgres/cutovers/"+url.PathEscape(id)+"/verify", nil, &out)
	return out, err
}
func (c *Client) CancelManagedPostgresCutover(ctx context.Context, id string) (ManagedPostgresCutover, error) {
	var out ManagedPostgresCutover
	err := c.do(ctx, http.MethodPost, "/v1/postgres/cutovers/"+url.PathEscape(id)+"/cancel", nil, &out)
	return out, err
}
