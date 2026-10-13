package state

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func recoveryAutomaticEvaluation(previous *api.WorkflowRecoveryAutomaticEvaluation, reason string, now time.Time) *api.WorkflowRecoveryAutomaticEvaluation {
	e := &api.WorkflowRecoveryAutomaticEvaluation{EvaluatedAt: now, Reason: reason}
	if reason != "child_created" && reason != "result_recorded" && reason != "suppressed" {
		e.WaitingSince = &now
		if previous != nil && previous.Reason == reason && previous.WaitingSince != nil {
			e.WaitingSince = previous.WaitingSince
		}
	}
	return e
}

func recoveryAutomaticEvaluationJSON(previous []byte, reason string, now time.Time) ([]byte, error) {
	var e *api.WorkflowRecoveryAutomaticEvaluation
	if len(previous) > 0 {
		if err := json.Unmarshal(previous, &e); err != nil {
			return nil, fmt.Errorf("decode automatic retry evaluation: %w", err)
		}
	}
	raw, err := json.Marshal(recoveryAutomaticEvaluation(e, reason, now))
	if err != nil {
		return nil, fmt.Errorf("encode automatic retry evaluation: %w", err)
	}
	return raw, nil
}
