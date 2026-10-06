// adr: 081
package sched

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowFinalOutputIsIndependentOfStorageOrder(t *testing.T) {
	now := time.Now().UTC()
	older := now.Add(-time.Second)
	specs := map[string]api.WorkflowStepSpec{
		"start":  {Name: "start"},
		"end":    {Name: "end", DependsOn: []string{"start"}},
		"middle": {Name: "middle"},
	}
	for _, tc := range []struct {
		name  string
		specs map[string]api.WorkflowStepSpec
		steps []*state.WorkflowStep
		want  string
	}{
		{"tied sequential and parallel", specs, []*state.WorkflowStep{
			{StepName: "start", FinishedAt: &now}, {StepName: "end", FinishedAt: &now}, {StepName: "middle", FinishedAt: &now},
		}, "middle"},
		{"parallel finishes later", specs, []*state.WorkflowStep{
			{StepName: "start", FinishedAt: &older}, {StepName: "end", FinishedAt: &older}, {StepName: "middle", FinishedAt: &now},
		}, "middle"},
		{"legacy without timestamps", specs, []*state.WorkflowStep{
			{StepName: "start"}, {StepName: "end"}, {StepName: "middle"},
		}, "middle"},
		{"known completion wins over legacy", specs, []*state.WorkflowStep{
			{StepName: "start"}, {StepName: "end", FinishedAt: &now}, {StepName: "middle"},
		}, "end"},
		{"timeout implicit dependency", map[string]api.WorkflowStepSpec{
			"wait": {Name: "wait", OnTimeout: "fallback"}, "fallback": {Name: "fallback"},
		}, []*state.WorkflowStep{{StepName: "wait", FinishedAt: &now}, {StepName: "fallback", FinishedAt: &now}}, "fallback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, step := range tc.steps {
				step.Status = state.WorkflowStepStatusSucceeded
				step.Output, _ = json.Marshal(step.StepName)
			}
			// Every cyclic rotation and its reverse must select the same value.
			for offset := range len(tc.steps) {
				for _, reverse := range []bool{false, true} {
					ordered := make([]*state.WorkflowStep, len(tc.steps))
					for i := range ordered {
						index := (i + offset) % len(tc.steps)
						if reverse {
							index = len(tc.steps) - 1 - index
						}
						ordered[i] = tc.steps[index]
					}
					want, _ := json.Marshal(tc.want)
					if got := workflowFinalOutput(ordered, tc.specs); string(got) != string(want) {
						t.Fatalf("offset=%d reverse=%v: output=%s, want %s", offset, reverse, got, want)
					}
				}
			}
		})
	}
}
