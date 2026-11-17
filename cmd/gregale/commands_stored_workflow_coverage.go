package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storedworkflowchecks"
)

type storedWorkflowCoverageHint = storedworkflowchecks.Hint
type storedWorkflowScenarioCoverage struct{ storedworkflowchecks.Coverage }

func (c *storedWorkflowScenarioCoverage) observe(r api.SimulateStoredWorkflowResponse) { c.Observe(r) }
func (c storedWorkflowScenarioCoverage) hints(d api.WorkflowSpec) []storedWorkflowCoverageHint {
	return c.Hints(d)
}
