package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationchecks"
)

type automationCoverageHint = automationchecks.Hint
type automationScenarioCoverage struct{ automationchecks.Coverage }

func (c *automationScenarioCoverage) observe(r api.SimulateAutomationResponse) { c.Observe(r) }
func (c automationScenarioCoverage) hints(d api.WorkflowSpec) []automationCoverageHint {
	return c.Hints(d)
}
