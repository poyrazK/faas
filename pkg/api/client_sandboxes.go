package api

import (
	"context"
	"net/url"
	"strconv"
)

// ListSandboxes lists the account's sandbox sessions, newest first
// (ADR-20261027090013). An empty state lists every state; before is the
// previous page's NextBefore.
func (c *Client) ListSandboxes(ctx context.Context, state, before string, limit int) (SandboxList, error) {
	q := url.Values{}
	if state != "" {
		q.Set("state", state)
	}
	if before != "" {
		q.Set("before", before)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/sandboxes"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out SandboxList
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetSandbox reads one session; a live session's view carries a fresh
// data-plane token.
func (c *Client) GetSandbox(ctx context.Context, id string) (Sandbox, error) {
	var out Sandbox
	return out, c.do(ctx, "GET", "/v1/sandboxes/"+url.PathEscape(id), nil, &out)
}

// PauseSandbox asks for a persistent session's pause (ADR-20261122132055);
// the session answers pausing until its capture is taken.
func (c *Client) PauseSandbox(ctx context.Context, id string) (Sandbox, error) {
	var out Sandbox
	return out, c.do(ctx, "POST", "/v1/sandboxes/"+url.PathEscape(id)+"/pause", struct{}{}, &out)
}

// ResumeSandbox asks for a paused persistent session's resume.
func (c *Client) ResumeSandbox(ctx context.Context, id string) (Sandbox, error) {
	var out Sandbox
	return out, c.do(ctx, "POST", "/v1/sandboxes/"+url.PathEscape(id)+"/resume", struct{}{}, &out)
}

// ForkSandbox starts sessions from a running or paused session's memory
// and volume (ADR-20261122132055 §3).
func (c *Client) ForkSandbox(ctx context.Context, id string, req ForkSandboxRequest) (ForkSandboxResponse, error) {
	var out ForkSandboxResponse
	return out, c.do(ctx, "POST", "/v1/sandboxes/"+url.PathEscape(id)+"/fork", req, &out)
}

// CloseSandbox closes a session; its VM, memory and workspace go with it.
func (c *Client) CloseSandbox(ctx context.Context, id string) (Sandbox, error) {
	var out Sandbox
	return out, c.do(ctx, "DELETE", "/v1/sandboxes/"+url.PathEscape(id), nil, &out)
}
