package state

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type profileCanaryHistoryCursor struct {
	DeploymentID        string    `json:"deployment_id"`
	CanaryStep          int       `json:"canary_step"`
	CanaryStepStartedAt time.Time `json:"canary_step_started_at"`
	PolicyRevision      int64     `json:"policy_revision"`
}

func encodeProfileCanaryHistoryCursor(deploymentID string, signal api.CanaryProfileSignal) string {
	cursor := profileCanaryHistoryCursor{DeploymentID: deploymentID, CanaryStep: signal.CanaryStep, CanaryStepStartedAt: signal.CanaryStepStartedAt.UTC(), PolicyRevision: signal.PolicyRevision}
	body, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeProfileCanaryHistoryCursor(value, deploymentID string) (profileCanaryHistoryCursor, error) {
	var cursor profileCanaryHistoryCursor
	if value == "" || len(value) > 512 {
		return cursor, ErrInvalidArgument
	}
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &cursor) != nil {
		return cursor, ErrInvalidArgument
	}
	id, err := uuid.Parse(cursor.DeploymentID)
	if err != nil || id.String() != cursor.DeploymentID || cursor.DeploymentID != deploymentID || cursor.CanaryStep < 0 || cursor.CanaryStep > math.MaxInt32 || cursor.CanaryStepStartedAt.IsZero() || cursor.PolicyRevision < 1 {
		return profileCanaryHistoryCursor{}, ErrInvalidArgument
	}
	canonicalJSON, err := json.Marshal(cursor)
	if err != nil || string(canonicalJSON) != string(body) || base64.RawURLEncoding.EncodeToString(body) != value {
		return profileCanaryHistoryCursor{}, ErrInvalidArgument
	}
	return cursor, nil
}

func validateProfileCanaryHistoryPage(limit int, before, deploymentID string) (*profileCanaryHistoryCursor, error) {
	if limit < 1 || limit > api.ProfileCanaryHistoryMaxPage {
		return nil, ErrInvalidArgument
	}
	if before == "" {
		return nil, nil
	}
	cursor, err := decodeProfileCanaryHistoryCursor(before, deploymentID)
	if err != nil {
		return nil, err
	}
	return &cursor, nil
}

func profileCanaryCursorMatches(cursor profileCanaryHistoryCursor, deploymentID string, signal api.CanaryProfileSignal) bool {
	return cursor.DeploymentID == deploymentID && cursor.CanaryStep == signal.CanaryStep && cursor.CanaryStepStartedAt.Equal(signal.CanaryStepStartedAt) && cursor.PolicyRevision == signal.PolicyRevision
}

func validateProfileCanaryHistorySignal(signal api.CanaryProfileSignal, deploymentID string) error {
	if (signal.Mode != "report_only" && signal.Mode != "gate") || signal.CanaryStep < 0 || signal.CanaryStepStartedAt.IsZero() || signal.PolicyRevision < 1 || signal.Candidate == nil || signal.Candidate.DeploymentID != deploymentID {
		return fmt.Errorf("%w: invalid canary profile history identity", ErrInvalidArgument)
	}
	return nil
}

func profileCanaryHistorySignalBefore(a, b api.CanaryProfileSignal) bool {
	if !a.CanaryStepStartedAt.Equal(b.CanaryStepStartedAt) {
		return a.CanaryStepStartedAt.After(b.CanaryStepStartedAt)
	}
	if a.CanaryStep != b.CanaryStep {
		return a.CanaryStep > b.CanaryStep
	}
	return a.PolicyRevision > b.PolicyRevision
}
