package e2e_test

// spec: §14 — source provenance, explicit native evidence and bounded cleanup.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestBindingNativeWorkflowIsolationAndEvidenceCustody(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github/workflows/e2e-native.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			If      string
			Timeout int `yaml:"timeout-minutes"`
			Steps   []struct {
				Name, Uses, If, Run string
				With                map[string]string
				ContinueOnError     bool `yaml:"continue-on-error"`
			}
		}
	}
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatal(err)
	}
	job, ok := workflow.Jobs["binding-native"]
	if !ok {
		t.Fatal("missing binding native job")
	}
	for _, condition := range []string{"github.event_name == 'workflow_dispatch'", "github.ref == 'refs/heads/main'", "inputs.lane == 'bindings'"} {
		if !strings.Contains(job.If, condition) {
			t.Errorf("binding job does not require %s", condition)
		}
	}
	if !strings.Contains(workflow.Jobs["e2e-native"].If, "inputs.lane != 'bindings'") {
		t.Error("platform matrix can overlap the binding job")
	}
	// The remote bound leaves source staging, evidence collection and node stop.
	if job.Timeout < 260 || job.Timeout > 360 {
		t.Errorf("unsafe binding job timeout: %d", job.Timeout)
	}
	collected, stopped, staged, executed, uploaded := false, false, false, false, false
	for _, step := range job.Steps {
		if step.ContinueOnError {
			t.Errorf("binding step %s can hide failure", step.Name)
		}
		if strings.Contains(step.Run, "package-binding-native-source.sh") {
			staged = true
		}
		if strings.Contains(step.Run, "run-binding-native.sh") {
			executed = true
			for _, required := range []string{"RuntimeMaxSec=270m", "FAAS_E2E_SOURCE_SHA", "FAAS_E2E_TRANSFER_ROOT", "tools/go/bin/go"} {
				if !strings.Contains(step.Run, required) {
					t.Errorf("native invocation missing %s", required)
				}
			}
		}
		if strings.Contains(step.Run, "binding-public.tar.gz") && strings.Contains(step.Run, "summary.json") {
			collected = true
			if !strings.Contains(step.If, "always()") || !strings.Contains(step.Run, "binding-evidence/public") {
				t.Error("evidence collection must preserve failure diagnostics and select only public reports")
			}
		}
		if strings.Contains(step.Uses, "actions/upload-artifact@") {
			uploaded = true
			if step.With["path"] != "${{ runner.temp }}/binding-evidence/*.json" || step.With["if-no-files-found"] != "error" {
				t.Error("binding artifacts may include private logs or omit missing evidence")
			}
		}
		if strings.Contains(step.Run, "instances stop") {
			stopped = true
			if !strings.Contains(step.If, "always()") || !strings.Contains(step.If, "keep_node_running != true") || strings.Contains(step.If, "started") {
				t.Error("node stop must survive failure/cancellation regardless of who started it")
			}
		}
	}
	if !collected || !stopped || !staged || !executed || !uploaded {
		t.Fatalf("incomplete lane: collect=%t stop=%t source=%t execute=%t upload=%t", collected, stopped, staged, executed, uploaded)
	}
	// The host registry permits only predeclared roots. New lane paths must fit
	// that existing policy; otherwise real staging fails before any guest runs.
	policyBytes, err := os.ReadFile(filepath.Join("..", "..", "deploy/ansible/roles/canary_artifact_retention/defaults/main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Patterns []string `yaml:"canary_artifact_patterns"`
	}
	if err := yaml.Unmarshal(policyBytes, &policy); err != nil {
		t.Fatal(err)
	}
	rootPattern := regexp.MustCompile(`remote_root="([^"]+)"`)
	for _, step := range job.Steps {
		for _, match := range rootPattern.FindAllStringSubmatch(step.Run, -1) {
			allowed := false
			for _, pattern := range policy.Patterns {
				if ok, _ := filepath.Match(pattern, match[1]); ok {
					allowed = true
				}
			}
			if !allowed {
				t.Errorf("binding stage root %s is outside the host artifact policy", match[1])
			}
		}
		if strings.Contains(step.Run, "faas-canary-artifacts finish") && !strings.Contains(step.If, "job.status == 'success'") {
			t.Error("failed private diagnostic bundle would be removed before operator inspection")
		}
	}
}

// The remote systemd bound must cover every serial suite's test alarm plus
// builds and teardown. Adding a suite must not silently erase cleanup time.
func TestBindingNativeBudgetCoversEveryRequiredSuite(t *testing.T) {
	root := filepath.Join("..", "..")
	data, err := os.ReadFile(filepath.Join(root, "scripts/ci/binding-native-suites.json"))
	if err != nil {
		t.Fatal(err)
	}
	var suites []struct {
		Runner string `json:"runner"`
	}
	if err := json.Unmarshal(data, &suites); err != nil {
		t.Fatal(err)
	}
	alarm := regexp.MustCompile(`-timeout ([0-9]+m)`)
	var total time.Duration
	for _, suite := range suites {
		data, err := os.ReadFile(filepath.Join(root, suite.Runner))
		if err != nil {
			t.Fatal(err)
		}
		matches := alarm.FindAllSubmatch(data, -1)
		if len(matches) != 1 {
			t.Fatalf("missing or ambiguous test alarm: %s", suite.Runner)
		}
		duration, err := time.ParseDuration(string(matches[0][1]))
		if err != nil {
			t.Fatal(err)
		}
		total += duration
	}
	data, err = os.ReadFile(filepath.Join(root, ".github/workflows/e2e-native.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct{ Steps []struct{ Run string } }
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	capRE := regexp.MustCompile(`RuntimeMaxSec=([0-9]+m)`)
	found := false
	for _, step := range workflow.Jobs["binding-native"].Steps {
		for _, match := range capRE.FindAllStringSubmatch(step.Run, -1) {
			duration, err := time.ParseDuration(match[1])
			if err != nil || duration < total+15*time.Minute {
				t.Fatal("native lane cap omits suite alarms or build/cleanup allowance")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("binding native lane has no remote cap")
	}
}
