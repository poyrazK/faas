package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A control plane rebuilt from backups cannot pass the control stage before a
// compute node exists, so cd-platform can skip that stage on request. The skip
// must never be implicit: a control stage that failed or was skipped without
// the input still stops every compute rollout and fails the verify gate.
func TestPlatformSkipsTheControlStageOnlyWhenAsked(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cd-platform.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		On struct {
			WorkflowDispatch struct {
				Inputs map[string]struct {
					Type    string `yaml:"type"`
					Default any    `yaml:"default"`
				} `yaml:"inputs"`
			} `yaml:"workflow_dispatch"`
		} `yaml:"on"`
		Jobs map[string]struct {
			If    string `yaml:"if"`
			Steps []struct{ Name, Run string }
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatal(err)
	}
	input, ok := workflow.On.WorkflowDispatch.Inputs["skip_control_stage"]
	if !ok || input.Type != "boolean" || input.Default != false {
		t.Fatalf("skip_control_stage must be a boolean input defaulting to false: %+v (present=%v)", input, ok)
	}
	if got := workflow.Jobs["control"].If; got != "${{ !inputs.skip_control_stage }}" {
		t.Fatalf("control stage condition = %q", got)
	}
	accepted := "(needs.control.result == 'success' || (inputs.skip_control_stage && needs.control.result == 'skipped'))"
	for _, job := range []string{"compute-prepare", "compute-prepare-compat", "compute"} {
		cond := workflow.Jobs[job].If
		if !strings.Contains(cond, accepted) {
			t.Fatalf("%s must accept a skipped control stage only with skip_control_stage: %q", job, cond)
		}
		if strings.Count(cond, "needs.control.result") != 2 {
			t.Fatalf("%s reads the control result outside the guarded clause: %q", job, cond)
		}
	}

	var verify string
	for _, step := range workflow.Jobs["verify"].Steps {
		if strings.Contains(step.Run, "control_ok=false") {
			verify = step.Run
		}
	}
	if verify == "" {
		t.Fatal("verify no longer derives control_ok")
	}
	if strings.Contains(verify, `"$CONTROL_RESULT" != "success"`) {
		t.Fatal("verify still compares CONTROL_RESULT directly, so a requested skip would fail the gate")
	}
	guard := `if [[ "$CONTROL_RESULT" == "success" || ( "$SKIP_CONTROL_STAGE" == "true" && "$CONTROL_RESULT" == "skipped" ) ]]; then control_ok=true; fi`
	if !strings.Contains(verify, guard) {
		t.Fatalf("verify must accept a skipped control stage only with skip_control_stage")
	}
}
