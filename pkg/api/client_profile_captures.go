package api

import (
	"context"
	"net/url"
	"time"
)

// GetAppHeapProfile returns the continuous live heap near query.End.
func (c *Client) GetAppHeapProfile(ctx context.Context, slug string, query ProfileQuery) (ProfileHeapResponse, error) {
	var out ProfileHeapResponse
	values := url.Values{"deployment_id": {query.DeploymentID}, "runtime": {query.Runtime}, "start": {query.Start.Format(time.RFC3339Nano)}, "end": {query.End.Format(time.RFC3339Nano)}}
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/profiles/heap?"+values.Encode(), nil, &out)
	return out, err
}

func profileCapturePath(slug string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/profiles/captures"
}

// CreateProfileCapture queues an on-demand capture (ADR-967). The returned
// capture is queued; poll GetProfileCapture until Done.
func (c *Client) CreateProfileCapture(ctx context.Context, slug string, req CreateProfileCaptureRequest) (ProfileCapture, error) {
	var out ProfileCapture
	err := c.do(ctx, "POST", profileCapturePath(slug), req, &out)
	return out, err
}

func (c *Client) ListProfileCaptures(ctx context.Context, slug string) (ProfileCaptureList, error) {
	var out ProfileCaptureList
	err := c.do(ctx, "GET", profileCapturePath(slug), nil, &out)
	return out, err
}

func (c *Client) GetProfileCapture(ctx context.Context, slug, id string) (ProfileCapture, error) {
	var out ProfileCapture
	err := c.do(ctx, "GET", profileCapturePath(slug)+"/"+url.PathEscape(id), nil, &out)
	return out, err
}

// GetProfileCaptureView returns one kind merged across the capture's processes.
func (c *Client) GetProfileCaptureView(ctx context.Context, slug, id, kind string) (ProfileCaptureView, error) {
	var out ProfileCaptureView
	err := c.do(ctx, "GET", profileCapturePath(slug)+"/"+url.PathEscape(id)+"/view?"+url.Values{"kind": {kind}}.Encode(), nil, &out)
	return out, err
}

// DownloadProfileCapture returns the merged gzip pprof profile of one kind.
func (c *Client) DownloadProfileCapture(ctx context.Context, slug, id, kind string) ([]byte, error) {
	var out []byte
	err := c.doBytes(ctx, "GET", profileCapturePath(slug)+"/"+url.PathEscape(id)+"/pprof?"+url.Values{"kind": {kind}}.Encode(), nil, &out)
	return out, err
}
