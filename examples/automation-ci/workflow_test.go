// adr: 1299
package automationci_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type workflowStep struct {
	Name            string            `yaml:"name"`
	Run             string            `yaml:"run"`
	If              string            `yaml:"if"`
	Env             map[string]string `yaml:"env"`
	ContinueOnError bool              `yaml:"continue-on-error"`
	Uses            string            `yaml:"uses"`
}

func workflowSteps(t *testing.T) []workflowStep {
	t.Helper()
	raw, err := os.ReadFile("github-actions.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		On          map[string]any    `yaml:"on"`
		Permissions map[string]string `yaml:"permissions"`
		Env         map[string]string `yaml:"env"`
		Jobs        map[string]struct {
			Env   map[string]string `yaml:"env"`
			Steps []workflowStep    `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	if len(workflow.On) != 1 || workflow.On["workflow_dispatch"] == nil || len(workflow.Permissions) != 0 || len(workflow.Env) != 0 || len(workflow.Jobs["gate"].Env) != 0 {
		t.Fatal("example is no longer an opt-in read with step-scoped credentials")
	}
	return workflow.Jobs["gate"].Steps
}

func TestWorkflowPreservesGateExitAndQuotesSelectors(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("example requires bash")
	}
	steps := workflowSteps(t)
	var gate workflowStep
	for _, step := range steps {
		if step.Name == "Evaluate retained suite evidence" {
			gate = step
		}
	}
	if gate.Run == "" || gate.ContinueOnError || !strings.Contains(gate.Env["FAAS_TOKEN"], "secrets.GREGALE_AUTOMATION_READ_TOKEN") || strings.Contains(gate.Run, "${{") {
		t.Fatal("gate action lost exit propagation or step-scoped input/credential handling")
	}
	for _, status := range []int{0, 1, 7, 2, 3, 4, 5, 6, 130} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			dir := t.TempDir()
			cliDir := filepath.Join(dir, "gregale")
			if err := os.Mkdir(cliDir, 0700); err != nil {
				t.Fatal(err)
			}
			stub := "#!/bin/bash\nset -euo pipefail\nprintf '%s\\n' \"$@\" > \"$GATE_CALLS\"\nprintf '{\"schema_version\":1,\"policy\":\"strict_v1\",\"exit_code\":%s}\\n' \"$FAKE_GATE_EXIT\"\nexit \"$FAKE_GATE_EXIT\"\n"
			if err := os.WriteFile(filepath.Join(cliDir, "gregale"), []byte(stub), 0700); err != nil {
				t.Fatal(err)
			}
			calls := filepath.Join(dir, "calls")
			sideEffect := filepath.Join(dir, "injected")
			name := "case/report; touch " + sideEffect
			cmd := exec.CommandContext(t.Context(), "bash", "-c", gate.Run)
			cmd.Env = append(os.Environ(), "RUNNER_TEMP="+dir, "FAAS_API=https://example.invalid", "FAAS_TOKEN=synthetic-read-token", "GATE_APP=billing", "GATE_NAME="+name, "GATE_BASELINE=baseline-id", "GATE_CANDIDATE=candidate-id", "GATE_VERSION=7", "GATE_PUBLISHED=4", "GATE_DEPLOYMENT=deployment-id", "GATE_CALLS="+calls, "FAKE_GATE_EXIT="+strconv.Itoa(status))
			output, err := cmd.CombinedOutput()
			actual := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatal(err)
				}
				actual = exit.ExitCode()
			}
			if actual != status {
				t.Fatalf("exit=%d want=%d output=%s", actual, status, output)
			}
			if _, err := os.Stat(sideEffect); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("selector was executed as shell code", err)
			}
			argv, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(argv), "--name\n"+name+"\n") || !strings.Contains(string(argv), "--non-interactive\nautomations\nlive\ngate\n") || strings.Contains(string(argv), "confirm-real-actions") {
				t.Fatal("workflow changed selectors or invoked execution", string(argv))
			}
			raw, err := os.ReadFile(filepath.Join(dir, "automation-gate.json"))
			if err != nil {
				t.Fatal(err)
			}
			var report struct {
				ExitCode int `json:"exit_code"`
			}
			if json.Unmarshal(raw, &report) != nil || report.ExitCode != status || strings.Contains(string(output)+string(raw), "synthetic-read-token") {
				t.Fatal("report was lost or a credential was exposed")
			}
		})
	}
}

func TestWorkflowAlwaysPublishesReportsWithoutSharingCredentials(t *testing.T) {
	steps := workflowSteps(t)
	summary, upload := false, false
	for _, step := range steps {
		if step.Name != "Evaluate retained suite evidence" && step.Env["FAAS_TOKEN"] != "" {
			t.Fatal("read credential escaped its one API step")
		}
		if step.Name == "Install pinned Gregale CLI" && !strings.Contains(step.Run, "--version \"$GREGALE_CLI_VERSION\"") {
			t.Fatal("workflow installer lost its explicit release pin")
		}
		if step.Name == "Summarize the gate result" {
			summary = step.If == "always()"
		}
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			upload = step.If == "always()"
		}
	}
	if !summary || !upload {
		t.Fatal("failed gate results can no longer be reviewed")
	}
}
