package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// TaskToolRetirement retains independent authority for an atomic park request.
// Pending means native parking was requested, not that shutdown is complete.
type TaskToolRetirement struct {
	Qualification    TaskToolRecoveryQualification `json:"qualification"`
	State            string                        `json:"state"`
	Decision         string                        `json:"decision,omitempty"`
	AuthorizerKeyID  string                        `json:"authorizer_key_id,omitempty"`
	DecidedAt        *time.Time                    `json:"decided_at,omitempty"`
	ParkTransitionID string                        `json:"park_transition_id,omitempty"`
	ParkRequestedAt  *time.Time                    `json:"park_requested_at,omitempty"`
	Result           *TaskToolRetirementResult     `json:"result,omitempty"`
}

// TaskToolRetirementRequest is private trusted CLI metadata. The park route
// consumes approval with native intent; check/observe only reconciles shutdown.
type TaskToolRetirementRequest struct {
	Action                  string                         `json:"action,omitempty"`
	Qualification           *TaskToolRecoveryQualification `json:"qualification,omitempty"`
	QualificationSHA256     string                         `json:"qualification_sha256,omitempty"`
	ClaimToken              string                         `json:"claim_token,omitempty"`
	BindingsSHA256          string                         `json:"bindings_sha256,omitempty"`
	EvidenceSHA256          string                         `json:"evidence_sha256,omitempty"`
	CheckedAt               time.Time                      `json:"checked_at,omitempty"`
	ExpectedInstanceIDs     map[string][]string            `json:"expected_instance_ids,omitempty"`
	ExpectedMCPAccessPolicy *MCPAccessPolicyExpectation    `json:"expected_mcp_access_policy,omitempty"`
	ShutdownTimeoutMS       int64                          `json:"shutdown_timeout_ms,omitempty"`
}

// TaskToolRetirementResult binds the native park transition and historical
// observations of drained native instances and absent/expired Task registrations.
type TaskToolRetirementResult struct {
	OperationID         string    `json:"operation_id"`
	HandoverID          string    `json:"handover_id"`
	QualificationSHA256 string    `json:"qualification_sha256"`
	WorkerDeploymentID  string    `json:"worker_deployment_id"`
	ServingDeploymentID string    `json:"serving_deployment_id"`
	WorkerIDs           []string  `json:"worker_ids"`
	ParkTransitionID    string    `json:"park_transition_id"`
	EvidenceSHA256      string    `json:"evidence_sha256"`
	ObservedAt          time.Time `json:"observed_at"`
	RecordedAt          time.Time `json:"recorded_at"`
}

func (c *Client) ParkTaskToolRetirement(ctx context.Context, id string, in TaskToolRetirementRequest) (TaskToolReleaseIntentRecord, error) {
	var out TaskToolReleaseIntentRecord
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, "/v1/task-tool-release-intents/"+url.PathEscape(id)+"/recovery/retire", in, &out)
	return out, err
}
