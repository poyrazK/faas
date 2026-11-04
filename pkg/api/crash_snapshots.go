package api

// Crash snapshot wire shapes and problems (ADR-733).

import "net/http"

// CrashSnapshotSettingsRequest is PUT /v1/apps/{slug}/crash-snapshots/settings.
type CrashSnapshotSettingsRequest struct {
	Enabled bool `json:"enabled"`
}

// CrashSnapshotSettingsResponse is an app's crash snapshot opt-in.
type CrashSnapshotSettingsResponse struct {
	Enabled   bool    `json:"enabled"`
	UpdatedAt *string `json:"updated_at,omitempty"`
}

// CrashCaptureResponse is one crash capture. Storage keys are never
// returned. Timestamps are RFC3339Nano UTC.
type CrashCaptureResponse struct {
	ID           string          `json:"id"`
	AppID        string          `json:"app_id"`
	DeploymentID string          `json:"deployment_id"`
	Trigger      string          `json:"trigger"`
	StatusCode   *int            `json:"status_code,omitempty"`
	Route        string          `json:"route,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	Status       string          `json:"status"`
	MemBytes     *int64          `json:"mem_bytes,omitempty"`
	Failure      *AppForkFailure `json:"failure,omitempty"`
	RequestedAt  string          `json:"requested_at"`
	CapturedAt   *string         `json:"captured_at,omitempty"`
	ExpiresAt    *string         `json:"expires_at,omitempty"`
}

// CrashCaptureListResponse is GET /v1/apps/{slug}/crash-snapshots.
type CrashCaptureListResponse struct {
	Items []CrashCaptureResponse `json:"items"`
}

const (
	// CodeCrashSnapshotsNotEnabled: the operator has not enabled crash
	// snapshots on this control plane (FAAS_CRASH_SNAPSHOTS).
	CodeCrashSnapshotsNotEnabled = "crash_snapshots_not_enabled"
	// CodeCrashCaptureRefused: a capture is in flight, one was taken
	// within the cooldown, or the app has no running instance.
	CodeCrashCaptureRefused = "crash_capture_refused"
	// CodeCrashCaptureNotReady: the capture cannot be forked (not ready,
	// failed or expired).
	CodeCrashCaptureNotReady = "crash_capture_not_ready"
)

// ErrCrashSnapshotsNotEnabled answers every crash snapshot route until the
// operator turns the feature on.
func ErrCrashSnapshotsNotEnabled() *Problem {
	return NewProblem(http.StatusNotImplemented, CodeCrashSnapshotsNotEnabled,
		"Crash snapshots unavailable",
		"crash snapshots are not enabled on this control-plane host").
		WithDocs(docsBase + "/crash-snapshots")
}

// ErrCrashCaptureRefused explains a refused manual capture.
func ErrCrashCaptureRefused() *Problem {
	return NewProblem(http.StatusConflict, CodeCrashCaptureRefused,
		"Capture not started",
		"a capture is already running, one was taken in the last 10 minutes, or the app has no running instance").
		WithDocs(docsBase + "/crash-snapshots")
}

// ErrCrashCaptureNotReady is returned when forking a capture that is not
// ready.
func ErrCrashCaptureNotReady() *Problem {
	return NewProblem(http.StatusConflict, CodeCrashCaptureNotReady,
		"Capture not ready",
		"only a ready, unexpired crash snapshot can be opened as a fork").
		WithDocs(docsBase + "/crash-snapshots")
}
