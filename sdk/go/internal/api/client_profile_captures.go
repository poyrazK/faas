package api

import (
	"context"
	"net/url"
	"time"
)

// GetAppHeapProfile returns the continuous live heap near query.End.
func (c *PreviewClient) GetAppHeapProfile(ctx context.Context, slug string, query ProfileQuery) (ProfileHeapResponse, error) {
	var out ProfileHeapResponse
	values := url.Values{"deployment_id": {query.DeploymentID}, "runtime": {query.Runtime}, "start": {query.Start.Format(time.RFC3339Nano)}, "end": {query.End.Format(time.RFC3339Nano)}}
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/profiles/heap?"+values.Encode(), nil, &out)
	return out, err
}

func profileCapturePath(slug string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/profiles/captures"
}

// CreateProfileCapture queues an on-demand capture (ADR-1155). The returned
// capture is queued; poll GetProfileCapture until Done.
func (c *PreviewClient) CreateProfileCapture(ctx context.Context, slug string, req CreateProfileCaptureRequest) (ProfileCapture, error) {
	var out ProfileCapture
	err := c.do(ctx, "POST", profileCapturePath(slug), req, &out)
	return out, err
}

func (c *PreviewClient) ListProfileCaptures(ctx context.Context, slug string) (ProfileCaptureList, error) {
	var out ProfileCaptureList
	err := c.do(ctx, "GET", profileCapturePath(slug), nil, &out)
	return out, err
}

func (c *PreviewClient) GetProfileCapture(ctx context.Context, slug, id string) (ProfileCapture, error) {
	var out ProfileCapture
	err := c.do(ctx, "GET", profileCapturePath(slug)+"/"+url.PathEscape(id), nil, &out)
	return out, err
}

// DeleteProfileCapture deletes one capture and its profiles, freeing its
// share of the account's capture storage.
func (c *PreviewClient) DeleteProfileCapture(ctx context.Context, slug, id string) error {
	return c.do(ctx, "DELETE", profileCapturePath(slug)+"/"+url.PathEscape(id), nil, nil)
}

// GetProfileCaptureView returns one kind merged across the capture's processes.
func (c *PreviewClient) GetProfileCaptureView(ctx context.Context, slug, id, kind string) (ProfileCaptureView, error) {
	var out ProfileCaptureView
	err := c.do(ctx, "GET", profileCapturePath(slug)+"/"+url.PathEscape(id)+"/view?"+url.Values{"kind": {kind}}.Encode(), nil, &out)
	return out, err
}

// DiffProfileCaptures compares one kind of capture id with the app's capture
// base; an empty kind lets the server pick a kind both collected.
func (c *PreviewClient) DiffProfileCaptures(ctx context.Context, slug, id, base, kind string) (ProfileCaptureDiff, error) {
	var out ProfileCaptureDiff
	query := url.Values{"base": {base}}
	if kind != "" {
		query.Set("kind", kind)
	}
	err := c.do(ctx, "GET", profileCapturePath(slug)+"/"+url.PathEscape(id)+"/diff?"+query.Encode(), nil, &out)
	return out, err
}
