package faas

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// CrashSnapshotEndpoint is the in-guest metadata endpoint for the ADR-733
// SDK trigger.
const CrashSnapshotEndpoint = "http://169.254.169.254/v1/crash-snapshots:capture"

// CrashSnapshotOptions tunes CaptureCrashSnapshot.
type CrashSnapshotOptions struct {
	// Reason is stored with the capture (at most 256 bytes).
	Reason string
	// Route is the failing request's path.
	Route string
	// Wait bounds how long the call blocks for the capture (default 15 s,
	// max 30 s).
	Wait time.Duration
	// Endpoint and HTTPClient override the defaults (tests).
	Endpoint   string
	HTTPClient *http.Client
}

// CrashSnapshotResult is the outcome of a capture request. Status is one of
// captured, pending, refused, failed, not_enabled, unavailable or
// invalid_request.
type CrashSnapshotResult struct {
	Status    string `json:"status"`
	CaptureID string `json:"capture_id,omitempty"`
	Code      string `json:"code,omitempty"`
	// InFork is true when this code is running in a fork restored from the
	// capture.
	InFork bool `json:"in_fork,omitempty"`
}

// CaptureCrashSnapshot asks Gregale to capture this instance now, from inside
// an error handler and before the error is handled, so the failing request's
// state is in the capture. It blocks while the instance is paused and
// snapshotted. In a fork of that capture it returns again with InFork set.
//
// The app must opt in to crash snapshots. It never returns an error: outside
// Gregale, or when the endpoint is unreachable, Status is "unavailable".
func CaptureCrashSnapshot(ctx context.Context, opts CrashSnapshotOptions) CrashSnapshotResult {
	endpoint, client := opts.Endpoint, opts.HTTPClient
	if endpoint == "" {
		endpoint = CrashSnapshotEndpoint
	}
	wait := opts.Wait
	if wait <= 0 {
		wait = 15 * time.Second
	}
	wait = min(wait, 30*time.Second)
	if client == nil {
		client = &http.Client{Timeout: wait + 20*time.Second}
	}
	body, err := json.Marshal(struct {
		Reason string `json:"reason,omitempty"`
		Route  string `json:"route,omitempty"`
		WaitMs int64  `json:"wait_ms,omitempty"`
	}{opts.Reason, opts.Route, wait.Milliseconds()})
	if err != nil {
		return CrashSnapshotResult{Status: "unavailable"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return CrashSnapshotResult{Status: "unavailable"}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return CrashSnapshotResult{Status: "unavailable"}
	}
	defer func() { _ = resp.Body.Close() }()
	var out CrashSnapshotResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Status == "" {
		return CrashSnapshotResult{Status: "unavailable"}
	}
	return out
}
