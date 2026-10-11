package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// workflowScheduleNominal prefers a fire in the current minute. Opt-in recovery
// coalesces older unconsumed fires into the latest one inside the bounded window.
// The caller consumes the whole interval through now, even on quota/overlap skip.
func workflowScheduleNominal(trigger api.WorkflowTriggerSpec, evaluated, now time.Time) (time.Time, error) {
	return api.WorkflowScheduleNominal(trigger, evaluated, now)
}
