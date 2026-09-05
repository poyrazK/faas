// Wire DTOs for the resumable upload protocol (issue #1182 §P1 PR-1).
//
// Server-only foundation; the CLI starts using these in PR-2. The
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

// UploadStartRequest is the JSON body of POST /v1/uploads. total_size
// is required and must be ≤ the per-plan SourceTarballMaxMB cap (Free
// / Hobby 100 MB, Pro / Scale 250 MB); the handler returns 413 +
// source_too_large otherwise. sha256_hex is recorded for the
// build_provenance audit row only — the server does NOT re-verify it
// at commit time (ADR-115 trust boundary).
type UploadStartRequest struct {
	AppSlug   string  `json:"app_slug"`
	TotalSize int64   `json:"total_size"`
	Sha256Hex *string `json:"sha256_hex,omitempty"`
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
