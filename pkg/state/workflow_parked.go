package state

import "github.com/onebox-faas/faas/pkg/api"

// workflowAdmission tallies an app's unfinished runs for the concurrency cap.
// Runs parked on an event count only beyond the plan's parked allowance
// (ADR-20261023215834), so an app waiting on many events can still start
// runs. Schedule overlap counts every unfinished run separately.
type workflowAdmission struct{ active, parked int }

func (a *workflowAdmission) add(status string) {
	switch status {
	case WorkflowRunStatusPending, WorkflowRunStatusRunning:
		a.active++
	case WorkflowRunStatusAwaitingEvent:
		a.parked++
	}
}

func (a workflowAdmission) admitted(maxParked int) int {
	return api.WorkflowAdmittedRuns(a.active, a.parked, maxParked)
}

// workflowAdmissionLocked is appID's count for the concurrency cap.
func (m *MemStore) workflowAdmissionLocked(appID string, maxParked int) int {
	var a workflowAdmission
	for _, run := range m.workflowRuns {
		if run.AppID == appID {
			a.add(run.Status)
		}
	}
	return a.admitted(maxParked)
}
