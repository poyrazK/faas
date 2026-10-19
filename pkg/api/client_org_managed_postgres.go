package api

import (
	"context"
	"net/http"
	"net/url"
)

func orgPostgresPath(organization string) string {
	return "/v1/orgs/" + url.PathEscape(organization) + "/postgres"
}

func (c *Client) GetOrgManagedPostgresCapabilities(ctx context.Context, organization string, region string) (ManagedPostgresCapabilities, error) {
	var out ManagedPostgresCapabilities
	path := orgPostgresPath(organization) + "/capabilities"
	if region != "" {
		path += "?region=" + url.QueryEscape(region)
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return ManagedPostgresCapabilities{}, err
	}
	return out, nil
}

func (c *Client) GetOrgManagedPostgresUsage(ctx context.Context, organization string) (ManagedPostgresUsageResponse, error) {
	var out ManagedPostgresUsageResponse
	err := c.do(ctx, http.MethodGet, orgPostgresPath(organization)+"/usage", nil, &out)
	return out, err
}

func (c *Client) ResizeOrgManagedPostgresDatabase(ctx context.Context, organization string, id string, request ResizeManagedPostgresDatabaseRequest) (ManagedPostgresResize, error) {
	var out ManagedPostgresResize
	err := c.do(ctx, http.MethodPost, orgPostgresPath(organization)+"/databases/"+url.PathEscape(id)+"/resize", request, &out)
	return out, err
}

func (c *Client) GetOrgManagedPostgresResize(ctx context.Context, organization string, databaseID, resizeID string) (ManagedPostgresResize, error) {
	var out ManagedPostgresResize
	err := c.do(ctx, http.MethodGet, orgPostgresPath(organization)+"/databases/"+url.PathEscape(databaseID)+"/resizes/"+url.PathEscape(resizeID), nil, &out)
	return out, err
}

func (c *Client) ListOrgManagedPostgresDatabases(ctx context.Context, organization string) (ManagedPostgresDatabaseList, error) {
	var out ManagedPostgresDatabaseList
	err := c.do(ctx, http.MethodGet, orgPostgresPath(organization)+"/databases", nil, &out)
	return out, err
}

func (c *Client) CreateOrgManagedPostgresDatabase(ctx context.Context, organization string, req CreateManagedPostgresDatabaseRequest) (ManagedPostgresDatabase, error) {
	var out ManagedPostgresDatabase
	err := c.do(ctx, http.MethodPost, orgPostgresPath(organization)+"/databases", req, &out)
	return out, err
}

func (c *Client) GetOrgManagedPostgresDatabase(ctx context.Context, organization string, id string) (ManagedPostgresDatabase, error) {
	var out ManagedPostgresDatabase
	err := c.do(ctx, http.MethodGet, orgPostgresPath(organization)+"/databases/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) DeleteOrgManagedPostgresDatabase(ctx context.Context, organization string, id string) (ManagedPostgresDatabase, error) {
	var out ManagedPostgresDatabase
	err := c.do(ctx, http.MethodDelete, orgPostgresPath(organization)+"/databases/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) RestoreOrgManagedPostgresDatabase(ctx context.Context, organization string, id string, req RestoreManagedPostgresDatabaseRequest) (ManagedPostgresDatabase, error) {
	var out ManagedPostgresDatabase
	err := c.do(ctx, http.MethodPost, orgPostgresPath(organization)+"/databases/"+url.PathEscape(id)+"/restore", req, &out)
	return out, err
}

func (c *Client) ListOrgManagedPostgresBindings(ctx context.Context, organization string, databaseID string) (ManagedPostgresBindingList, error) {
	var out ManagedPostgresBindingList
	err := c.do(ctx, http.MethodGet, orgPostgresPath(organization)+"/databases/"+url.PathEscape(databaseID)+"/bindings", nil, &out)
	return out, err
}

func (c *Client) CreateOrgManagedPostgresBinding(ctx context.Context, organization string, databaseID string, req CreateManagedPostgresBindingRequest) (ManagedPostgresBinding, error) {
	var out ManagedPostgresBinding
	err := c.do(ctx, http.MethodPost, orgPostgresPath(organization)+"/databases/"+url.PathEscape(databaseID)+"/bindings", req, &out)
	return out, err
}

func (c *Client) GetOrgManagedPostgresBinding(ctx context.Context, organization string, id string) (ManagedPostgresBinding, error) {
	var out ManagedPostgresBinding
	err := c.do(ctx, http.MethodGet, orgPostgresPath(organization)+"/bindings/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) DeleteOrgManagedPostgresBinding(ctx context.Context, organization string, id string) (ManagedPostgresBinding, error) {
	var out ManagedPostgresBinding
	err := c.do(ctx, http.MethodDelete, orgPostgresPath(organization)+"/bindings/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) RotateOrgManagedPostgresBinding(ctx context.Context, organization string, id string) (ManagedPostgresBinding, error) {
	var out ManagedPostgresBinding
	err := c.do(ctx, http.MethodPost, orgPostgresPath(organization)+"/bindings/"+url.PathEscape(id)+"/rotate", nil, &out)
	return out, err
}

func (c *Client) ChangeOrgManagedPostgresComputePolicy(ctx context.Context, organization string, id string, request ChangeManagedPostgresComputePolicyRequest) (ManagedPostgresComputePolicyChange, error) {
	var out ManagedPostgresComputePolicyChange
	err := c.do(ctx, http.MethodPost, orgPostgresPath(organization)+"/databases/"+url.PathEscape(id)+"/compute-policy", request, &out)
	return out, err
}

func (c *Client) GetOrgManagedPostgresComputePolicyChange(ctx context.Context, organization string, databaseID, changeID string) (ManagedPostgresComputePolicyChange, error) {
	var out ManagedPostgresComputePolicyChange
	err := c.do(ctx, http.MethodGet, orgPostgresPath(organization)+"/databases/"+url.PathEscape(databaseID)+"/compute-policy-changes/"+url.PathEscape(changeID), nil, &out)
	return out, err
}
