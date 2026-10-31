package api_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowCodeSpecDecodesYAMLAndJSON(t *testing.T) {
	var fromYAML api.WorkflowSpec
	if err := yaml.Unmarshal([]byte(`
name: order_followup
trigger:
  type: manual
code:
  run: order_followup
  timeout: 90s
  retry:
    max_attempts: 4
    backoff: exponential
`), &fromYAML); err != nil {
		t.Fatal(err)
	}
	if fromYAML.Code == nil || fromYAML.Code.Run != "order_followup" || fromYAML.Code.Timeout != 90*time.Second || fromYAML.Code.Retry.MaxAttempts != 4 {
		t.Fatalf("yaml code = %+v", fromYAML.Code)
	}
	raw, err := json.Marshal(fromYAML)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"steps":[]`) || !strings.Contains(string(raw), `"timeout":"1m30s"`) {
		t.Fatalf("marshal = %s", raw)
	}
	var fromJSON api.WorkflowSpec
	if err := json.Unmarshal(raw, &fromJSON); err != nil {
		t.Fatal(err)
	}
	if fromJSON.Code == nil || *fromJSON.Code.Retry != *fromYAML.Code.Retry || fromJSON.Code.Timeout != fromYAML.Code.Timeout || len(fromJSON.Steps) != 0 {
		t.Fatalf("round trip = %+v", fromJSON.Code)
	}
	if err := json.Unmarshal([]byte(`{"run":"x","timout":"1s"}`), &api.WorkflowCodeSpec{}); err == nil {
		t.Fatal("unknown code field accepted")
	}
	var day api.WorkflowCodeSpec
	if err := json.Unmarshal([]byte(`{"path":"/wf","timeout":"1d"}`), &day); err != nil || day.Timeout != 24*time.Hour {
		t.Fatalf("day timeout = %v %v", day.Timeout, err)
	}
}

func TestValidateWorkflowCode(t *testing.T) {
	valid := api.WorkflowSpec{Name: "wf", Code: &api.WorkflowCodeSpec{Run: "order_followup"}}
	if order, err := api.ValidateWorkflowDAG(valid, api.PlanHobby); err != nil || order != nil {
		t.Fatalf("valid code workflow: %v %v", order, err)
	}
	if _, err := api.ValidateWorkflowDAG(api.WorkflowSpec{Name: "wf", Code: &api.WorkflowCodeSpec{Path: "/workflows/order"}}, api.PlanHobby); err != nil {
		t.Fatalf("path entry: %v", err)
	}
	cases := map[string]struct {
		spec api.WorkflowSpec
		plan api.Plan
		want error
	}{
		"with steps":      {api.WorkflowSpec{Name: "wf", Code: &api.WorkflowCodeSpec{Run: "a"}, Steps: []api.WorkflowStepSpec{{Name: "s", Run: "s"}}}, api.PlanHobby, api.ErrWorkflowCodeInvalid},
		"run and path":    {api.WorkflowSpec{Name: "wf", Code: &api.WorkflowCodeSpec{Run: "a", Path: "/a"}}, api.PlanHobby, api.ErrWorkflowCodeInvalid},
		"no entry":        {api.WorkflowSpec{Name: "wf", Code: &api.WorkflowCodeSpec{}}, api.PlanHobby, api.ErrWorkflowCodeInvalid},
		"bad run":         {api.WorkflowSpec{Name: "wf", Code: &api.WorkflowCodeSpec{Run: "9lives"}}, api.PlanHobby, api.ErrWorkflowInvalidRun},
		"bad path":        {api.WorkflowSpec{Name: "wf", Code: &api.WorkflowCodeSpec{Path: "wf"}}, api.PlanHobby, api.ErrWorkflowInvalidPath},
		"long timeout":    {api.WorkflowSpec{Name: "wf", Code: &api.WorkflowCodeSpec{Run: "a", Timeout: time.Hour}}, api.PlanHobby, api.ErrWorkflowTimeoutExceeded},
		"bad retry":       {api.WorkflowSpec{Name: "wf", Code: &api.WorkflowCodeSpec{Run: "a", Retry: &api.WorkflowRetrySpec{MaxAttempts: 30}}}, api.PlanHobby, api.ErrWorkflowRetryInvalid},
		"action limit":    {api.WorkflowSpec{Name: "wf", MaxConcurrentActions: 2, Code: &api.WorkflowCodeSpec{Run: "a"}}, api.PlanHobby, api.ErrWorkflowCodeUnsupported},
		"plan disallowed": {api.WorkflowSpec{Name: "wf", Code: &api.WorkflowCodeSpec{Run: "a"}}, api.PlanFree, api.ErrWorkflowPlanNotAllowed},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := api.ValidateWorkflowDAG(tc.spec, tc.plan); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestWorkflowCodeHelpers(t *testing.T) {
	if api.WorkflowCodePath(api.WorkflowCodeSpec{Run: "order"}) != "/order" || api.WorkflowCodePath(api.WorkflowCodeSpec{Path: "/wf/order"}) != "/wf/order" {
		t.Fatal("entry path")
	}
	if retry := api.WorkflowCodeRetry(api.WorkflowCodeSpec{}); retry.MaxAttempts != api.WorkflowCodeStepDefaultMaxAttempts || retry.Backoff != "exponential" {
		t.Fatalf("default retry %+v", retry)
	}
	if api.WorkflowCodeDriverMaxAttempts(api.WorkflowCodeSpec{Retry: &api.WorkflowRetrySpec{MaxAttempts: 1}}) != api.WorkflowCodeDriverDefaultMaxAttempts ||
		api.WorkflowCodeDriverMaxAttempts(api.WorkflowCodeSpec{Retry: &api.WorkflowRetrySpec{MaxAttempts: 9}}) != 9 {
		t.Fatal("driver attempts")
	}
	for _, name := range []string{"charge", "poll#2", "a/b:c-d.e_f", strings.Repeat("x", 128)} {
		if !api.ValidWorkflowCodeOpName(name) {
			t.Errorf("%q rejected", name)
		}
	}
	for _, name := range []string{"", "$driver", "-x", "_foreach.x", "has space", "ü", strings.Repeat("x", 129)} {
		if api.ValidWorkflowCodeOpName(name) {
			t.Errorf("%q accepted", name)
		}
	}
}
