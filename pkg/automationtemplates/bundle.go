package automationtemplates

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"gopkg.in/yaml.v3"
)

// Bundle supplies the same definition and checked samples to the CLI and console.
type Bundle struct {
	Definition api.WorkflowSpec
	Sample     api.SimulateAutomationRequest
	Scenarios  []api.AutomationPublishCheckScenario
}

type starterScenario struct {
	Name             string                    `yaml:"name"`
	InputFile        string                    `yaml:"input_file"`
	MockOutputsFile  string                    `yaml:"mock_outputs_file"`
	MockAttemptsFile string                    `yaml:"mock_attempts_file"`
	Expect           map[string]map[string]any `yaml:"expect"`
}

// Load only reads embedded, named starters; it never accepts a filesystem path.
func Load(name string) (Bundle, error) {
	var out Bundle
	if !slices.Contains(Names, name) {
		return out, fmt.Errorf("unknown automation template %q", name)
	}
	source, err := fs.Sub(assets, name)
	if err != nil {
		return out, err
	}
	definition, err := fs.ReadFile(source, "automation.yaml")
	if err != nil {
		return out, err
	}
	if err := yaml.Unmarshal(definition, &out.Definition); err != nil {
		return out, err
	}
	suite, err := fs.ReadFile(source, "scenarios.yaml")
	if err != nil {
		return out, err
	}
	var scenarios struct {
		Scenarios []starterScenario `yaml:"scenarios"`
	}
	if err := yaml.Unmarshal(suite, &scenarios); err != nil {
		return out, err
	}
	for _, scenario := range scenarios.Scenarios {
		sample, err := starterSample(source, out.Definition, scenario)
		if err != nil {
			return out, err
		}
		check := api.AutomationPublishCheckScenario{Name: scenario.Name, Simulation: sample}
		steps := make([]string, 0, len(scenario.Expect))
		for step := range scenario.Expect {
			steps = append(steps, step)
		}
		sort.Strings(steps)
		for _, step := range steps {
			raw, err := json.Marshal(scenario.Expect[step])
			if err != nil {
				return out, err
			}
			var expectation api.AutomationCheckExpectation
			if err := json.Unmarshal(raw, &expectation); err != nil {
				return out, err
			}
			expectation.Step = step
			check.Expectations = append(check.Expectations, expectation)
		}
		out.Scenarios = append(out.Scenarios, check)
	}
	if len(out.Scenarios) == 0 {
		return out, fmt.Errorf("automation template has no scenarios")
	}
	out.Sample = out.Scenarios[0].Simulation
	return out, nil
}

func starterSample(source fs.FS, definition api.WorkflowSpec, scenario starterScenario) (api.SimulateAutomationRequest, error) {
	out := api.SimulateAutomationRequest{Definition: definition}
	input, err := fs.ReadFile(source, scenario.InputFile)
	if err != nil {
		return out, err
	}
	out.Input = input
	for _, file := range []struct {
		name   string
		target any
	}{
		{scenario.MockOutputsFile, &out.MockOutputs},
		{scenario.MockAttemptsFile, &out.MockAttempts},
	} {
		if file.name == "" {
			continue
		}
		raw, err := fs.ReadFile(source, file.name)
		if err != nil {
			return out, err
		}
		if err := json.Unmarshal(raw, file.target); err != nil {
			return out, err
		}
	}
	return out, nil
}
