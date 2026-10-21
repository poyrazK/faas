package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
)

// Resolution records an operator's SQL/execution checks after a verified new
// release is active. It does not execute SQL or change the cleanup task result.
type ProjectMigrationRecoveryRequest struct {
	RequestID            string `json:"request_id"`
	ExpectedTaskID       string `json:"expected_task_id"`
	ExpectedGeneration   uint64 `json:"expected_generation"`
	ReplacementAttemptID string `json:"replacement_attempt_id"`
	DatabaseStateChecked bool   `json:"database_state_checked"`
	ExecutionQuiescent   bool   `json:"execution_quiescent"`
	EvidenceHash         string `json:"evidence_hash"`
}

func (r ProjectMigrationRecoveryRequest) Valid() bool {
	id, err := uuid.Parse(r.ExpectedTaskID)
	decoded, hashErr := hex.DecodeString(r.EvidenceHash)
	return ValidDeploymentAttemptID(r.RequestID) && ValidDeploymentAttemptID(r.ReplacementAttemptID) && err == nil && id != uuid.Nil && id.String() == r.ExpectedTaskID && r.ExpectedGeneration > 0 && r.ExpectedGeneration <= math.MaxInt64 && r.DatabaseStateChecked && r.ExecutionQuiescent && hashErr == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == r.EvidenceHash
}
func (r ProjectMigrationRecoveryRequest) Fingerprint() string {
	payload, _ := json.Marshal(r)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

type ProjectMigrationRecovery struct {
	Status               string     `json:"status"`
	Reason               string     `json:"reason,omitempty"`
	RequestID            string     `json:"request_id,omitempty"`
	TaskID               string     `json:"task_id,omitempty"`
	ExpectedGeneration   uint64     `json:"expected_generation,omitempty"`
	ReplacementAttemptID string     `json:"replacement_attempt_id,omitempty"`
	ReplacementReleaseID string     `json:"replacement_release_id,omitempty"`
	EvidenceHash         string     `json:"evidence_hash,omitempty"`
	OperatorAccountID    string     `json:"operator_account_id,omitempty"`
	ResolvedAt           *time.Time `json:"resolved_at,omitempty"`
}

func (c *Client) ResolveProjectMigrationContract(ctx context.Context, attempt string, request ProjectMigrationRecoveryRequest) (ProjectSubmissionReceipt, error) {
	var result ProjectSubmissionReceipt
	if !ValidDeploymentAttemptID(attempt) || attempt == request.ReplacementAttemptID || !request.Valid() {
		return result, fmt.Errorf("invalid migration recovery request")
	}
	return result, c.do(ctx, "POST", "/v1/project-submissions/"+attempt+"/resolve-contract", request, &result)
}
