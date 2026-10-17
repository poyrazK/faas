// adr: 1387
package automationconsole

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationtemplates"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func TestSavedDraftComparisonHandlesReorderedJSONWithoutLosingPrecision(t *testing.T) {
	bundle, err := automationtemplates.Load("webhook-enrichment")
	if err != nil {
		t.Fatal(err)
	}
	spec := bundle.Definition
	spec.Trigger.Filter = json.RawMessage(`{"data":{"region":"eu","id":9007199254740993}}`)
	spec.Steps[0].Input = json.RawMessage(`{"array":[1,2],"id":9007199254740993}`)
	data := dashboard.AutomationConsoleData{Name: spec.Name, Definition: pretty(spec), Version: 3}
	for _, test := range []struct {
		name, filter, input string
		version             int64
		valid               bool
	}{
		{"reordered keys", `{"data":{"id":9007199254740993,"region":"eu"}}`, `{"id":9007199254740993,"array":[1,2]}`, 3, true},
		{"changed large number", `{"data":{"id":9007199254740992,"region":"eu"}}`, string(spec.Steps[0].Input), 3, false},
		{"changed array order", string(spec.Trigger.Filter), `{"id":9007199254740993,"array":[2,1]}`, 3, false},
		{"stale revision", string(spec.Trigger.Filter), string(spec.Steps[0].Input), 4, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var saved api.WorkflowSpec
			if err := json.Unmarshal([]byte(data.Definition), &saved); err != nil {
				t.Fatal(err)
			}
			saved.Trigger.Filter, saved.Steps[0].Input = json.RawMessage(test.filter), json.RawMessage(test.input)
			host := &inspectionHost{responses: map[string]Response{"get": inspectionJSON(t, api.AutomationResponse{Name: spec.Name, Version: test.version, Draft: saved})}}
			service := Service{Host: host}
			_, failure := service.savedDraft(httptest.NewRequest(http.MethodGet, "/", nil), Scope{}, data)
			if (failure == nil) != test.valid {
				t.Fatalf("saved-draft comparison valid=%v, failure=%+v", test.valid, failure)
			}
		})
	}
}
