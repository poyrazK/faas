package api

import (
	"context"
	"net/http"
	"net/url"
)

func crashSnapshotsPath(slug string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/crash-snapshots"
}

// GetCrashSnapshotSettings reads whether 5xx crash snapshots are on (ADR-733).
func (c *Client) GetCrashSnapshotSettings(ctx context.Context, slug string) (CrashSnapshotSettingsResponse, error) {
	var out CrashSnapshotSettingsResponse
	return out, c.do(ctx, http.MethodGet, crashSnapshotsPath(slug)+"/settings", nil, &out)
}

// PutCrashSnapshotSettings turns 5xx crash snapshots on or off.
func (c *Client) PutCrashSnapshotSettings(ctx context.Context, slug string, req CrashSnapshotSettingsRequest) (CrashSnapshotSettingsResponse, error) {
	var out CrashSnapshotSettingsResponse
	return out, c.do(ctx, http.MethodPut, crashSnapshotsPath(slug)+"/settings", req, &out)
}

// ListCrashSnapshots returns the app's crash snapshots, newest first.
func (c *Client) ListCrashSnapshots(ctx context.Context, slug string) (CrashCaptureListResponse, error) {
	var out CrashCaptureListResponse
	return out, c.do(ctx, http.MethodGet, crashSnapshotsPath(slug), nil, &out)
}

// CreateCrashSnapshot captures the app's newest running instance now.
func (c *Client) CreateCrashSnapshot(ctx context.Context, slug string) (CrashCaptureResponse, error) {
	var out CrashCaptureResponse
	return out, c.do(ctx, http.MethodPost, crashSnapshotsPath(slug), nil, &out)
}

// GetCrashSnapshot reads one crash snapshot.
func (c *Client) GetCrashSnapshot(ctx context.Context, slug, id string) (CrashCaptureResponse, error) {
	var out CrashCaptureResponse
	return out, c.do(ctx, http.MethodGet, crashSnapshotsPath(slug)+"/"+url.PathEscape(id), nil, &out)
}

// ForkCrashSnapshot opens a ready crash snapshot as a production fork.
func (c *Client) ForkCrashSnapshot(ctx context.Context, slug, id string, req CreateAppForkRequest) (AppForkResponse, error) {
	var out AppForkResponse
	return out, c.do(ctx, http.MethodPost, crashSnapshotsPath(slug)+"/"+url.PathEscape(id)+"/fork", req, &out)
}
