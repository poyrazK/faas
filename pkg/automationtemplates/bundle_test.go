// adr: 1285
package automationtemplates_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationtemplates"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestConsoleBundlesUseCompleteStarterScenarios(t *testing.T) {
	for _, name := range automationtemplates.Names {
		t.Run(name, func(t *testing.T) {
			bundle, err := automationtemplates.Load(name)
			if err != nil {
				t.Fatal(err)
			}
			expected, _ := json.Marshal(bundle.Definition)
			if len(bundle.Scenarios) == 0 {
				t.Fatal("starter has no publishing checks")
			}
			for _, scenario := range bundle.Scenarios {
				actual, _ := json.Marshal(scenario.Simulation.Definition)
				if !bytes.Equal(expected, actual) {
					t.Fatal("scenario uses a different definition")
				}
				result, err := state.SimulateAutomation(t.Context(), scenario.Simulation, api.PlanPro)
				if err != nil || !result.DefinitionValid || !result.Complete || len(result.Issues) != 0 {
					t.Fatalf("scenario %s: %+v %v", scenario.Name, result, err)
				}
				trace := map[string]api.AutomationSimulationStep{}
				for _, step := range result.Trace {
					trace[step.StepName] = step
				}
				for _, expectation := range scenario.Expectations {
					step, ok := trace[expectation.Step]
					if !ok || step.State != expectation.State {
						t.Fatalf("%s/%s expected %s, got %s", scenario.Name, expectation.Step, expectation.State, step.State)
					}
					if expectation.WhenMatched != nil && (step.WhenMatched == nil || *step.WhenMatched != *expectation.WhenMatched) {
						t.Fatal("guard expectation differs from the CLI starter")
					}
				}
			}
		})
	}
	if _, err := automationtemplates.Load("../../secrets"); err == nil {
		t.Fatal("accepted a non-starter path")
	}
}
