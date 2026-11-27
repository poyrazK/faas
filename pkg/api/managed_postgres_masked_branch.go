package api

import (
	"context"
	"net/http"
	"net/url"
)

const ManagedPostgresMaskedPolicyMaxBytes = 32 << 10

type CreateManagedPostgresMaskedBranchRequest struct {
	Name       string                       `json:"name"`
	TTLSeconds int64                        `json:"ttl_seconds,omitempty"`
	Tables     []ManagedPostgresMaskedTable `json:"tables"`
}

type ManagedPostgresMaskedTable struct {
	Name    string                        `json:"name"`
	Columns []ManagedPostgresMaskedColumn `json:"columns"`
}

type ManagedPostgresMaskedColumn struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}

func (c *Client) CreateManagedPostgresMaskedBranch(ctx context.Context, source string, req CreateManagedPostgresMaskedBranchRequest) (ManagedPostgresDatabase, error) {
	return c.createManagedPostgresMaskedBranch(ctx, "/v1/postgres", source, req)
}

func (c *Client) CreateOrgManagedPostgresMaskedBranch(ctx context.Context, org, source string, req CreateManagedPostgresMaskedBranchRequest) (ManagedPostgresDatabase, error) {
	return c.createManagedPostgresMaskedBranch(ctx, orgPostgresPath(org), source, req)
}

func (c *Client) createManagedPostgresMaskedBranch(ctx context.Context, prefix, source string, req CreateManagedPostgresMaskedBranchRequest) (ManagedPostgresDatabase, error) {
	var out ManagedPostgresDatabase
	err := c.do(ctx, http.MethodPost, prefix+"/databases/"+url.PathEscape(source)+"/masked-branches", req, &out)
	return out, err
}
