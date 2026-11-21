package agents_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261027090016 amendment 1 — a version's capability policy names only
// tools the gateway has, in allow or deny mode, with a tool-call budget
// within the plan; fields whose rollout step has not shipped are refused, so
// no seller believes a rule is enforced that is not.
func TestCreateAgentCapabilityPolicy(t *testing.T) {
	const base = `{"name":"t","harness":{"preset":"codex"},"prompt_template":"p","capability_policy":`
	for _, tc := range []struct {
		name, plan, policy string
		status             int
		code               string
		want               *api.AgentCapabilityPolicy
	}{
		{"allow web_fetch", "pro", `{"tools":[{"name":"web_fetch","mode":"allow"}]}`, http.StatusCreated, "",
			&api.AgentCapabilityPolicy{Tools: []api.AgentCapabilityTool{{Name: "web_fetch", Mode: "allow"}}}},
		{"budget", "pro", `{"tools":[{"name":"web_fetch","mode":"allow"}],"budgets":{"tool_calls":200}}`, http.StatusCreated, "",
			&api.AgentCapabilityPolicy{Tools: []api.AgentCapabilityTool{{Name: "web_fetch", Mode: "allow"}},
				Budgets: &api.AgentCapabilityBudgets{ToolCalls: 200}}},
		{"empty is dropped", "pro", `{}`, http.StatusCreated, "", nil},
		{"deny only", "pro", `{"tools":[{"name":"web_fetch","mode":"deny"}]}`, http.StatusCreated, "",
			&api.AgentCapabilityPolicy{Tools: []api.AgentCapabilityTool{{Name: "web_fetch", Mode: "deny"}}}},
		{"unknown tool", "pro", `{"tools":[{"name":"web_fetches","mode":"allow"}]}`, http.StatusBadRequest, api.CodeValidation, nil},
		{"named twice", "pro", `{"tools":[{"name":"web_fetch","mode":"allow"},{"name":"web_fetch","mode":"deny"}]}`, http.StatusBadRequest, api.CodeValidation, nil},
		{"approve", "pro", `{"tools":[{"name":"web_fetch","mode":"approve"}]}`, http.StatusCreated, "",
			&api.AgentCapabilityPolicy{Tools: []api.AgentCapabilityTool{{Name: "web_fetch", Mode: "approve"}}}},
		{"bad mode", "pro", `{"tools":[{"name":"web_fetch","mode":"maybe"}]}`, http.StatusBadRequest, api.CodeValidation, nil},
		{"negative budget", "pro", `{"budgets":{"tool_calls":-1}}`, http.StatusBadRequest, api.CodeValidation, nil},
		{"budget over plan", "hobby", `{"budgets":{"tool_calls":1001}}`, http.StatusForbidden, api.CodeAgentLimit, nil},
		{"taint not yet", "pro", `{"taint_mode":"deny"}`, http.StatusBadRequest, api.CodeValidation, nil},
		{"sensitive not yet", "pro", `{"sensitive":["web_fetch"]}`, http.StatusBadRequest, api.CodeValidation, nil},
		{"web not yet", "pro", `{"web":{"tier":1}}`, http.StatusBadRequest, api.CodeValidation, nil},
		{"data not yet", "pro", `{"data":{}}`, http.StatusBadRequest, api.CodeValidation, nil},
		{"unknown field", "pro", `{"tools":[],"memory":true}`, http.StatusBadRequest, api.CodeValidation, nil},
	} {
		store := &fakeStore{}
		rec := serve(newHandler(store).CreateAgent, api.Plan(tc.plan), http.MethodPost, "/v1/agents", base+tc.policy+`}`, nil)
		if rec.Code != tc.status {
			t.Errorf("%s: status %d, want %d: %s", tc.name, rec.Code, tc.status, rec.Body)
			continue
		}
		if tc.code != "" && problemCode(t, rec) != tc.code {
			t.Errorf("%s: code %s, want %s", tc.name, problemCode(t, rec), tc.code)
		}
		if tc.status == http.StatusCreated && !reflect.DeepEqual(store.spec.CapabilityPolicy, tc.want) {
			t.Errorf("%s: stored policy %+v, want %+v", tc.name, store.spec.CapabilityPolicy, tc.want)
		}
	}
}
