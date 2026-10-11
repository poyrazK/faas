package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func automationDiffSpec(t *testing.T, document string) api.WorkflowSpec {
	t.Helper()
	var spec api.WorkflowSpec
	if err := json.Unmarshal([]byte(document), &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestAutomationDefinitionDiff(t *testing.T) {
	const baseline = `{"name":"flow","steps":[{"name":"first","path":"/first"},{"name":"second","path":"/second","depends_on":["first"]}]}`
	for _, tc := range []struct {
		name, draft string
		want        []automationDefinitionChange
	}{
		{"unchanged", baseline, []automationDefinitionChange{}},
		{"added", `{"name":"flow","steps":[{"name":"first","path":"/first"},{"name":"new","path":"/new"},{"name":"second","path":"/second","depends_on":["first"]}]}`, []automationDefinitionChange{{"/step_order", "modified"}, {"/steps/new", "added"}}},
		{"removed", `{"name":"flow","steps":[{"name":"first","path":"/first"}]}`, []automationDefinitionChange{{"/step_order", "modified"}, {"/steps/second", "removed"}}},
		{"renamed", `{"name":"flow","steps":[{"name":"first","path":"/first"},{"name":"renamed","path":"/second","depends_on":["first"]}]}`, []automationDefinitionChange{{"/step_order", "modified"}, {"/steps/renamed", "added"}, {"/steps/second", "removed"}}},
		{"reordered", `{"name":"flow","steps":[{"name":"second","path":"/second","depends_on":["first"]},{"name":"first","path":"/first"}]}`, []automationDefinitionChange{{"/step_order", "modified"}}},
		{"dependency", `{"name":"flow","steps":[{"name":"first","path":"/first"},{"name":"second","path":"/second"}]}`, []automationDefinitionChange{{"/steps/second/depends_on", "removed"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			published := automationDiffSpec(t, baseline)
			draft := automationDiffSpec(t, tc.draft)
			before, _ := json.Marshal(draft)
			report, err := diffAutomationDefinition(api.AutomationResponse{Name: "flow", Version: 7, PublishedVersion: 4, Enabled: true, Draft: draft, Published: &published})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(report.Changes, tc.want) || report.Changed != (len(tc.want) > 0) || report.FirstPublication || report.DraftVersion != 7 || report.PublishedVersion != 4 || !report.LiveEnabled {
				t.Fatalf("diff=%+v want=%v", report, tc.want)
			}
			after, _ := json.Marshal(draft)
			if string(before) != string(after) {
				t.Fatal("comparison mutated draft")
			}
		})
	}
}

func TestAutomationDefinitionDiffOperationalFields(t *testing.T) {
	before := automationDiffSpec(t, `{"name":"flow","trigger":{"type":"schedule","schedule":"0 9 * * *","timezone":"UTC","enabled":false},"steps":[{"name":"send","outbound":{"integration_id":"old","path":"/old"},"input":{"mapping":"old"},"retry":{"max_attempts":2,"backoff":"fixed"},"on_failure":"old-handler"},{"name":"approval","wait_for_event":"old-event","timeout":"1h","on_timeout":"old-timeout"}]}`)
	after := automationDiffSpec(t, `{"name":"flow","trigger":{"type":"schedule","schedule":"0 10 * * *","timezone":"Europe/Paris","enabled":true},"steps":[{"name":"send","outbound":{"integration_id":"new","path":"/new"},"input":{"mapping":"new"},"retry":{"max_attempts":3,"backoff":"exponential"},"on_failure":"new-handler"},{"name":"approval","wait_for_event":"new-event","timeout":"2h","on_timeout":"new-timeout"}]}`)
	report, err := diffAutomationDefinition(api.AutomationResponse{Draft: after, Published: &before})
	if err != nil {
		t.Fatal(err)
	}
	want := []automationDefinitionChange{
		{"/steps/approval/on_timeout", "modified"}, {"/steps/approval/timeout", "modified"}, {"/steps/approval/wait_for_event", "modified"},
		{"/steps/send/input/mapping", "modified"}, {"/steps/send/on_failure", "modified"}, {"/steps/send/outbound/integration_id", "modified"}, {"/steps/send/outbound/path", "modified"},
		{"/steps/send/retry/backoff", "modified"}, {"/steps/send/retry/max_attempts", "modified"},
		{"/trigger/enabled", "modified"}, {"/trigger/schedule", "modified"}, {"/trigger/timezone", "modified"},
	}
	if !reflect.DeepEqual(report.Changes, want) {
		t.Fatalf("changes=%v want=%v", report.Changes, want)
	}
}

func TestAutomationDefinitionDiffInputSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, old, next string
		want            []automationDefinitionChange
	}{
		{"exact_number", `{"value":9007199254740992}`, `{"value":9007199254740993}`, []automationDefinitionChange{{"/steps/send/input/value", "modified"}}},
		{"equivalent_number", `{"value":1}`, `{"value":1.0}`, []automationDefinitionChange{}},
		{"object_order", `{"a":1,"b":2}`, `{"b":2,"a":1}`, []automationDefinitionChange{}},
		{"array_order", `[1,2]`, `[2,1]`, []automationDefinitionChange{{"/steps/send/input", "modified"}}},
		{"null_added", `{}`, `{"value":null}`, []automationDefinitionChange{{"/steps/send/input/value", "added"}}},
		{"null_removed", `{"value":null}`, `{}`, []automationDefinitionChange{{"/steps/send/input/value", "removed"}}},
		{"escaped_path", `{"a/b~c":1}`, `{"a/b~c":2}`, []automationDefinitionChange{{"/steps/send/input/a~1b~0c", "modified"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := api.WorkflowSpec{Name: "flow", Steps: []api.WorkflowStepSpec{{Name: "send", Path: "/send", Input: json.RawMessage(tc.old)}}}
			after := api.WorkflowSpec{Name: "flow", Steps: []api.WorkflowStepSpec{{Name: "send", Path: "/send", Input: json.RawMessage(tc.next)}}}
			report, err := diffAutomationDefinition(api.AutomationResponse{Draft: after, Published: &before})
			if err != nil || !reflect.DeepEqual(report.Changes, tc.want) {
				t.Fatalf("diff=%+v err=%v want=%v", report, err, tc.want)
			}
		})
	}
}

func TestAutomationDefinitionDiffFirstPublicationAndInvalid(t *testing.T) {
	draft := automationDiffSpec(t, `{"name":"flow","steps":[{"name":"send","path":"/send"}]}`)
	report, err := diffAutomationDefinition(api.AutomationResponse{Draft: draft})
	if err != nil || !report.FirstPublication || !report.Changed || !reflect.DeepEqual(report.Changes, []automationDefinitionChange{{"/name", "added"}, {"/step_order", "added"}, {"/steps", "added"}}) {
		t.Fatalf("diff=%+v err=%v", report, err)
	}
	draft.Steps = append(draft.Steps, draft.Steps[0])
	if _, err := diffAutomationDefinition(api.AutomationResponse{Draft: draft}); err == nil {
		t.Fatal("accepted duplicate names")
	}
	draft.Steps = draft.Steps[:1]
	draft.Steps[0].Input = json.RawMessage(`invalid`)
	if _, err := diffAutomationDefinition(api.AutomationResponse{Draft: draft}); err == nil {
		t.Fatal("accepted invalid input JSON")
	}
}

func TestCmdAutomationsDiff(t *testing.T) {
	for _, mode := range []string{"human", "json", "unchanged", "first", "wrong_name", "invalid_args", "service_error"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = mode == "json"
			output := captureAutomationStdout(t)
			before := automationDiffSpec(t, `{"name":"flow","steps":[{"name":"send","path":"/old","input":{"token":"old-secret-value"}}]}`)
			after := automationDiffSpec(t, `{"name":"flow","steps":[{"name":"send","path":"/new","input":{"token":"new-secret-value"}}]}`)
			response := api.AutomationResponse{Name: "flow", Version: 7, PublishedVersion: 4, Draft: after, Published: &before}
			if mode == "first" {
				response.Published = nil
				response.PublishedVersion = 0
			}
			if mode == "unchanged" {
				response.Draft = before
			}
			if mode == "wrong_name" {
				response.Name = "other"
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != "GET" || r.URL.Path != "/v1/apps/billing/automations/flow" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				if mode == "service_error" {
					w.WriteHeader(503)
					_, _ = w.Write([]byte(`{}`))
					return
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "token")
			args := []string{"diff", "--app", "billing", "--name", "flow"}
			if mode == "invalid_args" {
				args = []string{"diff", "--app", "billing"}
			}
			code := cmdAutomations(args)
			if mode == "wrong_name" || mode == "invalid_args" || mode == "service_error" {
				if code == 0 {
					t.Fatal("expected failure")
				}
				if mode == "invalid_args" && requests != 0 {
					t.Fatal("invalid arguments reached API")
				}
				return
			}
			if code != 0 || requests != 1 {
				t.Fatalf("exit=%d requests=%d output=%s", code, requests, output)
			}
			if strings.Contains(output.String(), "secret-value") || strings.Contains(output.String(), "/new") {
				t.Fatalf("report leaked values: %s", output)
			}
			if mode == "json" {
				var report automationDefinitionDiff
				if err := json.Unmarshal(output.Bytes(), &report); err != nil || len(report.Changes) != 2 {
					t.Fatalf("report=%+v err=%v", report, err)
				}
			}
			if mode == "unchanged" && !strings.Contains(output.String(), "No definition changes") {
				t.Fatal(output.String())
			}
			if mode == "first" && !strings.Contains(output.String(), "First publication") {
				t.Fatal(output.String())
			}
		})
	}
}

func TestAutomationDefinitionDiffGuardsAndEventTriggers(t *testing.T) {
	before := automationDiffSpec(t, `{"name":"flow","trigger":{"type":"event","source":"old-source","event_type":"old-event","filter":{"amount":[1]}},"steps":[{"name":"send","path":"/send","when":{"ref":"input.status","op":"eq","value":"old-private-value"}}]}`)
	after := automationDiffSpec(t, `{"name":"flow","trigger":{"type":"event","source":"new-source","event_type":"new-event","filter":{"amount":[2]}},"steps":[{"name":"send","path":"/send","when":{"ref":"input.status","op":"eq","value":"new-private-value"}}]}`)
	report, err := diffAutomationDefinition(api.AutomationResponse{Draft: after, Published: &before})
	want := []automationDefinitionChange{{"/steps/send/when/value", "modified"}, {"/trigger/event_type", "modified"}, {"/trigger/filter/amount", "modified"}, {"/trigger/source", "modified"}}
	if err != nil || !reflect.DeepEqual(report.Changes, want) {
		t.Fatalf("diff=%+v err=%v want=%v", report, err, want)
	}
	for _, jsonMode := range []bool{false, true} {
		resetJSONOut(t)
		jsonOutput = jsonMode
		output := captureAutomationStdout(t)
		if code := printAutomationDefinitionDiff(report); code != 0 {
			t.Fatal(code)
		}
		if strings.Contains(output.String(), "private-value") || strings.Contains(output.String(), "new-source") || strings.Contains(output.String(), "old-source") {
			t.Fatalf("leaked definition values: %s", output)
		}
	}
}
