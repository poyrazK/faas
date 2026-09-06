// Wire DTOs for the resumable upload protocol (issue #1182 §P1 PR-1).
//
// Shared wire DTOs for the resumable upload server and CLI. The
// schema parity in cmd/apid/spec_compliance_test.go scans pkg/api/*.go
// for struct names matching api/openapi.yaml components.schemas —
// every UploadStart{Request,Response} added to the spec must have a
// matching type here, or the spec gate fails.
//
// Field naming and types match the handler's local DTOs in
// cmd/apid/handlers_upload_session.go:88-105 (startUploadRequest +
// startUploadResponse). Daemon-neutral: no imports of pkg/state,
// apid, or schedd — same pattern as diff.go / trigger.go.

package api

// UploadDeployOptions are the deployment fields that travel with a
// resumable source upload. Keeping them on the session makes commit retries
// deterministic and prevents the resumable path from silently dropping CLI
// metadata.
type UploadDeployOptions struct {
	Runtime    string         `json:"runtime,omitempty"`
	Handler    string         `json:"handler,omitempty"`
	Dockerfile bool           `json:"dockerfile,omitempty"`
	SourceRoot string         `json:"source_root,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	Tag        string         `json:"tag,omitempty"`
	DeployedBy string         `json:"deployed_by,omitempty"`
	PRNumber   int            `json:"pr_number,omitempty"`
	Workflows  []WorkflowSpec `json:"workflows,omitempty"`
}

// UploadStartRequest is the JSON body of POST /v1/uploads. total_size
// is required and must be ≤ the per-plan SourceTarballMaxMB cap (Free
// / Hobby 100 MB, Pro / Scale 250 MB); the handler returns 413 +
// source_too_large otherwise. sha256_hex is recorded for the
// build_provenance audit row only — the server does NOT re-verify it
// at commit time (ADR-115 trust boundary).
type UploadStartRequest struct {
	AppSlug       string               `json:"app_slug"`
	TotalSize     int64                `json:"total_size"`
	Sha256Hex     *string              `json:"sha256_hex,omitempty"`
	DeployOptions *UploadDeployOptions `json:"deploy_options,omitempty"`
}

// UploadStartResponse is the JSON body POST /v1/uploads returns.
// chunk_size is server-decided (8 MiB default; 16 MiB for Scale).
// expires_at is the 24h TTL stamp; the in-process reaper
// (cmd/apid/upload_session_reaper.go) flips status='open' rows
// past expires_at to 'expired' on a 5-min ticker.
type UploadStartResponse struct {
	UploadID  string `json:"upload_id"`
	ChunkSize int32  `json:"chunk_size"`
	TotalSize int64  `json:"total_size"`
	ExpiresAt string `json:"expires_at"`
}

// UploadSessionResponse is returned by GET /v1/uploads/{id}. It lets a
// client discover the server's current offset after a restart or lost reply.
type UploadSessionResponse struct {
	UploadID      string  `json:"upload_id"`
	AppSlug       string  `json:"app_slug"`
	ChunkSize     int32   `json:"chunk_size"`
	TotalSize     int64   `json:"total_size"`
	ReceivedBytes int64   `json:"received_bytes"`
	Status        string  `json:"status"`
	ExpiresAt     string  `json:"expires_at"`
	DeploymentID  *string `json:"deployment_id,omitempty"`
}
