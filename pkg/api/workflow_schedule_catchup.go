package api

import (
	"fmt"
	"time"
)

const (
	WorkflowScheduleCatchUpSkip   = "skip"
	WorkflowScheduleCatchUpLatest = "latest"
)

func (t WorkflowTriggerSpec) ScheduleCatchUpPolicy() string {
	if t.CatchUp == "" {
		return WorkflowScheduleCatchUpSkip
	}
	return t.CatchUp
}

// ScheduleCatchUpWindow validates the policy and returns its bounded recovery
// window. A zero window means missed occurrences are discarded.
func (t WorkflowTriggerSpec) ScheduleCatchUpWindow() (time.Duration, error) {
	switch t.ScheduleCatchUpPolicy() {
	case WorkflowScheduleCatchUpSkip:
		if t.CatchUpWindow != "" {
			return 0, fmt.Errorf("%w: catch_up_window requires catch_up: latest", ErrWorkflowInvalidTrigger)
		}
		return 0, nil
	case WorkflowScheduleCatchUpLatest:
		if t.CatchUpWindow == "" {
			return WorkflowScheduleCatchUpWindowDefault, nil
		}
		window, err := time.ParseDuration(t.CatchUpWindow)
		if err != nil || window < WorkflowScheduleCatchUpWindowMin || window > WorkflowScheduleCatchUpWindowMax {
			return 0, fmt.Errorf("%w: catch_up_window must be a duration between %s and %s", ErrWorkflowInvalidTrigger, WorkflowScheduleCatchUpWindowMin, WorkflowScheduleCatchUpWindowMax)
		}
		return window, nil
	default:
		return 0, fmt.Errorf("%w: catch_up must be skip or latest", ErrWorkflowInvalidTrigger)
	}
}
