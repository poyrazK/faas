package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Client is a thin typed wrapper over the v1 REST API. It renders the API's
// RFC 7807 problems into the CLI's three-line error shape (UX §3.3) rather than
// inventing copy.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient builds a client for baseURL with a bearer token.
func NewClient(baseURL, token string) *Client {
	return &Client{baseURL: baseURL, token: token, http: &http.Client{Timeout: 30 * time.Second}}
}

// APIError carries a server problem for the CLI to render.
type APIError struct{ Problem api.Problem }

func (e *APIError) Error() string {
	p := e.Problem
	if p.DocsURL != "" {
		return fmt.Sprintf("%s\n  %s\n  → %s", p.Title, p.Detail, p.DocsURL)
	}
	return fmt.Sprintf("%s\n  %s", p.Title, p.Detail)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, r)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var p api.Problem
		if json.Unmarshal(data, &p) == nil && p.Code != "" {
			return &APIError{Problem: p}
		}
		return fmt.Errorf("API error: %s", resp.Status)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Whoami returns the authenticated account.
func (c *Client) Whoami(ctx context.Context) (api.AccountResponse, error) {
	var out api.AccountResponse
	return out, c.do(ctx, "GET", "/v1/account", nil, &out)
}

// ListApps returns the account's apps.
func (c *Client) ListApps(ctx context.Context) ([]api.AppResponse, error) {
	var out []api.AppResponse
	return out, c.do(ctx, "GET", "/v1/apps", nil, &out)
}

// CreateApp creates an app.
func (c *Client) CreateApp(ctx context.Context, req api.CreateAppRequest) (api.AppResponse, error) {
	var out api.AppResponse
	return out, c.do(ctx, "POST", "/v1/apps", req, &out)
}

// Deploy creates a deployment for an app slug.
func (c *Client) Deploy(ctx context.Context, slug string, req api.CreateDeploymentRequest) (api.DeploymentResponse, error) {
	var out api.DeploymentResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/deployments", req, &out)
}
