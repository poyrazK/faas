package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Recovery records database inspection separately from the original task outcome.
type ProjectPromotionMigrationRecoveryRequest struct {
	RequestID              string `json:"request_id"`
	ExpectedTaskID         string `json:"expected_task_id"`
	ExpectedGeneration     int64  `json:"expected_generation"`
	Action                 string `json:"action"`
	ReplacementPromotionID string `json:"replacement_promotion_id,omitempty"`
	DatabaseStateChecked   bool   `json:"database_state_checked"`
	SchemaCompatible       bool   `json:"schema_compatible"`
	EvidenceHash           string `json:"evidence_hash"`
}

func (r ProjectPromotionMigrationRecoveryRequest) Valid() bool {
	task, err := uuid.Parse(r.ExpectedTaskID)
	hash, hashErr := hex.DecodeString(r.EvidenceHash)
	if !ValidDeploymentAttemptID(r.RequestID) || err != nil || task == uuid.Nil || task.String() != r.ExpectedTaskID || r.ExpectedGeneration <= 0 || !r.DatabaseStateChecked || hashErr != nil || len(hash) != sha256.Size || hex.EncodeToString(hash) != r.EvidenceHash {
		return false
	}
	if r.Action == "resume" {
		return r.SchemaCompatible && r.ReplacementPromotionID == ""
	}
	replacement, replacementErr := uuid.Parse(r.ReplacementPromotionID)
	return r.Action == "resolve" && replacementErr == nil && replacement != uuid.Nil && (replacement.String() == r.ReplacementPromotionID || strings.ReplaceAll(replacement.String(), "-", "") == r.ReplacementPromotionID)
}

func (r ProjectPromotionMigrationRecoveryRequest) Fingerprint() string {
	raw, _ := json.Marshal(r)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

type ProjectPromotionMigrationRecovery struct {
	Request           ProjectPromotionMigrationRecoveryRequest `json:"request"`
	Status            string                                   `json:"status"`
	Reason            string                                   `json:"reason,omitempty"`
	OperatorAccountID string                                   `json:"operator_account_id"`
	RequestedAt       time.Time                                `json:"requested_at"`
	Deadline          time.Time                                `json:"deadline"`
	ResolvedAt        *time.Time                               `json:"resolved_at,omitempty"`
}

func (c *Client) RecoverProjectEnvironmentPromotionMigration(ctx context.Context, project, environment, promotion string, request ProjectPromotionMigrationRecoveryRequest) (ProjectPromotionMigrationRecovery, error) {
	var out ProjectPromotionMigrationRecovery
	if !ValidProjectSlug(project) || !ValidProjectEnvironmentSlug(environment) || !request.Valid() {
		return out, fmt.Errorf("invalid promotion migration recovery request")
	}
	path := "/v1/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/promotions/" + url.PathEscape(promotion) + "/recover-migration"
	return out, c.do(ctx, "POST", path, request, &out)
}
