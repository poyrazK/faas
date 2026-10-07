// adr: 645
package api

import (
	"encoding/json"
	"time"
)

const OperationJobRunHeader = "X-Gregale-Operation-Job-Run-Id"
const OperationJobInstanceHeader = "X-Gregale-Operation-Job-Instance-Id"
const OperationJobCapabilityHeader = "X-Gregale-Operation-Job-Capability"

// Available confirms a private verified copy, not business completion.
type OperationJobArtifactResponse struct {
	Available bool                     `json:"available"`
	Artifact  *OperationResultArtifact `json:"artifact,omitempty"`
}

// OperationArtifactUploadRequest describes direct private bytes. Gregale
// derives the reference; callers cannot supply a storage key or source URI.
type OperationArtifactUploadRequest struct {
	ReportID  string `json:"report_id"`
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

type OperationJobReportRequest struct {
	ReportID string                  `json:"report_id"`
	Progress *OperationReportRequest `json:"progress,omitempty"`
	Result   json.RawMessage         `json:"result,omitempty"`
}
type OperationJobControlResponse struct {
	AccountID             string    `json:"account_id"`
	AppID                 string    `json:"app_id"`
	PlatformTenantID      string    `json:"platform_tenant_id"`
	Scope                 string    `json:"scope"`
	OperationID           string    `json:"operation_id"`
	JobRunID              string    `json:"job_run_id"`
	Generation            int       `json:"generation"`
	Attempt               int       `json:"attempt"`
	CancellationRequested bool      `json:"cancellation_requested"`
	DeadlineAt            time.Time `json:"deadline_at"`
	LeaseExpiresAt        time.Time `json:"lease_expires_at"`
	ObservedAt            time.Time `json:"observed_at"`
	PollAfterMS           int       `json:"poll_after_ms"`
}
