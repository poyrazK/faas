package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// StreamAppLogs opens the GET /v1/apps/{slug}/logs stream and returns
// its raw response body. The response is text/event-stream; callers
// parse frames themselves with bufio.Scanner.
//
// follow=true keeps the stream open after the initial page (server
// keeps writing); follow=false closes it once the initial page has
// been emitted. The returned io.ReadCloser must be Closed by the
// caller — the API keeps the stream open server-side until either
// EOF or context cancellation.
//
// deploymentID filters to a specific deployment (matches the
// `?deployment=` query param the CLI's `faas logs --deployment` uses);
// pass "" to receive all instances' frames.
//
// Non-2xx responses are decoded as *APIError (same convention as the
// JSON methods) and the body is closed internally; the caller only
// ever sees a successful body or an error.
func (c *Client) StreamAppLogs(ctx context.Context, slug, deploymentID string, follow bool) (io.ReadCloser, error) {
	path := fmt.Sprintf("/v1/apps/%s/logs?follow=", slug)
	if follow {
		path += "1"
	} else {
		path += "0"
	}
	if deploymentID != "" {
		path += "&deployment=" + deploymentID
	}
	return c.stream(ctx, path)
}

// StreamDeploymentLogs opens GET /v1/deployments/{id}/logs. beforeSeq
// narrows the initial page to rows whose seq is strictly less than
// the cursor (server-side default is 0 = the most recent page); pass
// nil for "open from the live tail". limit caps the initial page
// (server-side default 50, max 500); pass 0 for the default.
//
// follow=true keeps the stream open after the initial page; the server
// emits periodic "event: log" frames as new build/deploy rows land.
// The server sends a single "event: end data: {}" before closing the
// stream when the deployment reaches a terminal status (or after a
// 10-minute backstop).
func (c *Client) StreamDeploymentLogs(ctx context.Context, id string, beforeSeq *int64, limit int, follow bool) (io.ReadCloser, error) {
	path := fmt.Sprintf("/v1/deployments/%s/logs?", id)
	q := ""
	if beforeSeq != nil {
		q += "&before_seq=" + fmt.Sprintf("%d", *beforeSeq)
	}
	if limit > 0 {
		q += "&limit=" + fmt.Sprintf("%d", limit)
	}
	if follow {
		q += "&follow=1"
	} else {
		q += "&follow=0"
	}
	if len(q) > 0 {
		// path already has "?"; strip leading "&"
		path += q[1:]
	}
	return c.stream(ctx, path)
}

// stream is the shared HTTP-execution backbone for both SSE helpers.
// It keeps the request body close discipline in one place so the
// public API can stay "return io.ReadCloser, error" without dragging
// response.closer semantics into every helper.
func (c *Client) stream(ctx context.Context, path string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.sseClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the API: %w", err)
	}
	if resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var p Problem
		if err := json.Unmarshal(data, &p); err == nil && p.Code != "" {
			return nil, &APIError{Problem: p}
		}
		return nil, fmt.Errorf("API error: %s", resp.Status)
	}
	return resp.Body, nil
}
