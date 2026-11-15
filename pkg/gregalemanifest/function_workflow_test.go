package gregalemanifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261113120824 §1: `workflow: true` or a mapping makes a function a
// code workflow on its own app, named after it, entered at "/", manual unless
// a trigger is given; the plan's workflow rules apply.
func TestFunctionWorkflowSpec(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		check      func(t *testing.T, spec api.WorkflowSpec)
	}{
		{"true", "name: orders\nworkflow: true\n", func(t *testing.T, spec api.WorkflowSpec) {
			if spec.Name != "orders" || spec.Trigger.Type != "manual" || spec.Code.Path != "/" || spec.Code.Timeout != 0 {
				t.Fatalf("spec = %+v code %+v", spec, spec.Code)
			}
		}},
		{"mapping", "name: orders\nworkflow:\n  timeout: 90s\n  retry: {max_attempts: 5, backoff: exponential}\n  max_concurrent_runs: 3\n  trigger: {type: schedule, schedule: \"0 * * * *\"}\n",
			func(t *testing.T, spec api.WorkflowSpec) {
				if spec.Code.Timeout != 90*time.Second || spec.Code.Retry.MaxAttempts != 5 || spec.MaxConcurrentRuns != 3 || spec.Trigger.Type != "schedule" {
					t.Fatalf("spec = %+v code %+v", spec, spec.Code)
				}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var fn FunctionSpec
			if err := yaml.Unmarshal([]byte(tc.yaml), &fn); err != nil {
				t.Fatal(err)
			}
			spec, ok, err := fn.WorkflowSpec()
			if err != nil || !ok {
				t.Fatalf("WorkflowSpec = %v, %v", ok, err)
			}
			tc.check(t, spec)
			if err := validateFunctionWorkflow(fn, api.PlanPro); err != nil {
				t.Fatalf("valid on Pro: %v", err)
			}
			if err := validateFunctionWorkflow(fn, api.PlanFree); err == nil || !strings.Contains(err.Error(), "does not allow workflows") {
				t.Fatalf("Free plan error = %v", err)
			}
		})
	}
	var off FunctionSpec
	if err := yaml.Unmarshal([]byte("name: a\nworkflow: false\n"), &off); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := off.WorkflowSpec(); ok || off.IsWorkflow() {
		t.Fatal("workflow: false declared a workflow")
	}
	var bad FunctionSpec
	if err := yaml.Unmarshal([]byte("name: a\nworkflow: {steps: []}\n"), &bad); err == nil || !strings.Contains(err.Error(), `unknown field "steps"`) {
		t.Fatalf("unknown key error = %v", err)
	}
}

// Config in code sets workflow like any other key.
func TestFunctionWorkflowFromCode(t *testing.T) {
	cfg, ok, err := ParseInlineConfig("handler.js", []byte("export const config = { workflow: { timeout: '2m' } };\nexport default async function handler(input, wf) {}\n"))
	if err != nil || !ok {
		t.Fatalf("ParseInlineConfig = %v, %v", ok, err)
	}
	spec, isWorkflow, err := cfg.Spec.WorkflowSpec()
	if err != nil || !isWorkflow || spec.Code.Timeout != 2*time.Minute {
		t.Fatalf("workflow from code = %+v %v %v", spec, isWorkflow, err)
	}
}

// A Node or Python workflow function must declare the SDK whose protocol
// core its adapter loads; Go and non-workflow functions need nothing.
func TestCheckWorkflowFunctionSDK(t *testing.T) {
	project := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(project, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wf := &FunctionWorkflow{Enabled: true}
	plan := func(root, runtime string, workflow *FunctionWorkflow) FunctionPlan {
		return FunctionPlan{Spec: FunctionSpec{Name: "orders", Workflow: workflow}, Root: root, Runtime: runtime}
	}
	if err := CheckWorkflowFunctionSDK(project, plan("fn", "node24", wf)); err == nil || !strings.Contains(err.Error(), "@gregale/sdk-node") {
		t.Fatalf("node without SDK = %v", err)
	}
	write("package.json", `{"dependencies":{"@gregale/sdk-node":"github:gregale-dev/gregale-js"}}`)
	if err := CheckWorkflowFunctionSDK(project, plan("fn", "node24", wf)); err != nil {
		t.Fatalf("SDK in the project package.json: %v", err)
	}
	if err := CheckWorkflowFunctionSDK(project, plan("py", "python312", wf)); err == nil || !strings.Contains(err.Error(), "gregale-sdk") {
		t.Fatalf("python without SDK = %v", err)
	}
	write("py/requirements.txt", "httpx\ngregale-sdk @ git+https://github.com/gregale-dev/gregale-python\n")
	if err := CheckWorkflowFunctionSDK(project, plan("py", "python312", wf)); err != nil {
		t.Fatalf("SDK in requirements.txt: %v", err)
	}
	for _, p := range []FunctionPlan{plan("go", "go124", wf), plan("plain", "node24", nil)} {
		if err := CheckWorkflowFunctionSDK(project, p); err != nil {
			t.Fatalf("%s: %v", p.Runtime, err)
		}
	}
}
