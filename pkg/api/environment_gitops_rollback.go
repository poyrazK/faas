package api

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Rollback plans propose code changes through Git; they never restore mutable
// data or carry approval, qualification or activation authority.
type PreviewEnvironmentGitRollbackRequest struct {
	ReleaseSetID     string `json:"release_set_id"`
	TargetCommitSHA  string `json:"target_commit_sha"`
	ExpectedPlanHash string `json:"expected_plan_hash,omitempty"`
}

type PreviewEnvironmentGitRollbackResponse struct {
	Project                     string                            `json:"project"`
	Environment                 string                            `json:"environment"`
	ObservedAt                  time.Time                         `json:"observed_at"`
	ReadOnly                    bool                              `json:"read_only"`
	ValuesRedacted              bool                              `json:"values_redacted"`
	RequiresTargetQualification bool                              `json:"requires_target_qualification"`
	CurrentReleaseSetID         string                            `json:"current_release_set_id"`
	Origin                      EnvironmentGitPromotionOrigin     `json:"origin"`
	OriginServedAt              *time.Time                        `json:"origin_served_at,omitempty"`
	Target                      EnvironmentGitPromotionTarget     `json:"target"`
	Artifacts                   []EnvironmentGitPromotionArtifact `json:"artifacts"`
	Changes                     []EnvironmentGitPromotionChange   `json:"changes"`
	TargetPlan                  EnvironmentGitOpsPlan             `json:"target_plan"`
	BlockingReasons             []string                          `json:"blocking_reasons"`
	PlanHash                    string                            `json:"plan_hash"`
}

func (r PreviewEnvironmentGitRollbackRequest) Validate() error {
	id, err := uuid.Parse(r.ReleaseSetID)
	if err != nil || id.String() != r.ReleaseSetID {
		return fmt.Errorf("select a canonical release UUID")
	}
	for _, value := range []struct {
		raw   string
		bytes int
	}{{r.TargetCommitSHA, 20}, {r.ExpectedPlanHash, 32}} {
		if value.bytes == 32 && value.raw == "" {
			continue
		}
		decoded, err := hex.DecodeString(value.raw)
		if err != nil || len(decoded) != value.bytes || strings.ToLower(value.raw) != value.raw {
			return fmt.Errorf("select an immutable target commit and valid review hash")
		}
	}
	return nil
}
