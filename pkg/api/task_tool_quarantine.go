package api

import "time"

// TaskToolQuarantine retains separate authority for one candidate claim-fence
// attempt. Applying is deliberately not proof that the external SQL committed.
type TaskToolQuarantine struct {
	Qualification   TaskToolRecoveryQualification `json:"qualification"`
	State           string                        `json:"state"`
	Decision        string                        `json:"decision,omitempty"`
	AuthorizerKeyID string                        `json:"authorizer_key_id,omitempty"`
	DecidedAt       *time.Time                    `json:"decided_at,omitempty"`
	AttemptSHA256   string                        `json:"attempt_sha256,omitempty"`
	ClaimedAt       *time.Time                    `json:"claimed_at,omitempty"`
	Result          *TaskToolQuarantineResult     `json:"result,omitempty"`
}

// TaskToolQuarantineRequest is private CLI metadata on the existing journal
// routes. Agent tools accept only public intent/operation/qualification IDs.
type TaskToolQuarantineRequest struct {
	Action                  string                         `json:"action,omitempty"`
	Qualification           *TaskToolRecoveryQualification `json:"qualification,omitempty"`
	QualificationSHA256     string                         `json:"qualification_sha256,omitempty"`
	ClaimToken              string                         `json:"claim_token,omitempty"`
	AttemptToken            string                         `json:"attempt_token,omitempty"`
	BindingsSHA256          string                         `json:"bindings_sha256,omitempty"`
	EvidenceSHA256          string                         `json:"evidence_sha256,omitempty"`
	CheckedAt               time.Time                      `json:"checked_at,omitempty"`
	ExpectedInstanceIDs     map[string][]string            `json:"expected_instance_ids,omitempty"`
	ExpectedMCPAccessPolicy *MCPAccessPolicyExpectation    `json:"expected_mcp_access_policy,omitempty"`
}

// TaskToolQuarantineResult records an authenticated observation of the exact
// candidate claim fences. It is historical evidence, not an atomic cross-database
// commit receipt, a drain completion, or current runtime-health certification.
type TaskToolQuarantineResult struct {
	OperationID         string    `json:"operation_id"`
	HandoverID          string    `json:"handover_id"`
	QualificationSHA256 string    `json:"qualification_sha256"`
	WorkerDeploymentID  string    `json:"worker_deployment_id"`
	ServingDeploymentID string    `json:"serving_deployment_id"`
	WorkerIDs           []string  `json:"worker_ids"`
	EvidenceSHA256      string    `json:"evidence_sha256"`
	ObservedAt          time.Time `json:"observed_at"`
	RecordedAt          time.Time `json:"recorded_at"`
}
