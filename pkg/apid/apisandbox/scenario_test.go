package apisandbox

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAPISandboxScenarioValidation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		seed   json.RawMessage
		method string
		path   string
		body   json.RawMessage
		status int
		ok     bool
	}{
		{name: "relative URL with a query", seed: json.RawMessage(`{}`), method: "GET", path: "/notes?cursor=a%2Fb", status: 200, ok: true},
		{name: "documented HTTP failure", seed: json.RawMessage(`{}`), method: "POST", path: "/notes", body: json.RawMessage(`{"body":"duplicate"}`), status: 409, ok: true},
		{name: "other host", seed: json.RawMessage(`{}`), method: "GET", path: "//example.test/notes", status: 200},
		{name: "fragment in target", seed: json.RawMessage(`{}`), method: "GET", path: "/notes?id=42#fragment", status: 200},
		{name: "invalid query escape", seed: json.RawMessage(`{}`), method: "GET", path: "/notes?id=%GG", status: 200},
		{name: "GET body", seed: json.RawMessage(`{}`), method: "GET", path: "/notes", body: json.RawMessage(`{}`), status: 200},
		{name: "seed is not an object", seed: json.RawMessage(`[]`), method: "GET", path: "/notes", status: 200},
		{name: "missing seed", method: "GET", path: "/notes", status: 200},
		{name: "oversized fixture", seed: json.RawMessage(`{"test":"` + strings.Repeat("a", api.APISandboxSeedMaxBytes) + `"}`), method: "GET", path: "/notes", status: 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := api.APISandboxScenarioInput{
				Seed: tt.seed,
				APISandboxScenarioGuide: api.APISandboxScenarioGuide{
					Name: "Notes fixture",
					Requests: []api.APISandboxExampleRequest{{
						Title: "Test call", Method: tt.method, Path: tt.path, Body: tt.body, ExpectedStatus: tt.status,
					}},
				},
			}
			if err := ValidateScenario(&input); (err == nil) != tt.ok {
				t.Fatalf("validation error = %v, want valid = %v", err, tt.ok)
			}
		})
	}
}

func TestAPISandboxStripeWebhookValidatesFixtureAndFinalCheck(t *testing.T) {
	valid := api.APISandboxScenarioInput{
		Seed: json.RawMessage(`{}`),
		StripeWebhook: &api.APISandboxStripeWebhook{
			DeliveryPath: "/internal/stripe", EventType: "invoice.paid", DataObject: json.RawMessage(`{"id":"in_demo"}`),
		},
		APISandboxScenarioGuide: api.APISandboxScenarioGuide{
			Name: "Stripe fixture",
			Requests: []api.APISandboxExampleRequest{{
				Title: "Confirm subscription", Method: "GET", Path: "/subscriptions/demo", ExpectedStatus: 200,
				Assertions: []api.APISandboxJSONAssertion{{Pointer: "/active", Equals: json.RawMessage(`true`)}},
			}},
		},
	}
	if err := ValidateScenario(&valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*api.APISandboxScenarioInput)
	}{
		{"external endpoint", func(in *api.APISandboxScenarioInput) { in.StripeWebhook.DeliveryPath = "//elsewhere.test/hook" }},
		{"query on delivery route", func(in *api.APISandboxScenarioInput) { in.StripeWebhook.DeliveryPath += "?x=1" }},
		{"wildcard event name", func(in *api.APISandboxScenarioInput) { in.StripeWebhook.EventType = "invoice.*" }},
		{"array fixture", func(in *api.APISandboxScenarioInput) { in.StripeWebhook.DataObject = json.RawMessage(`[]`) }},
		{"mutating assertion", func(in *api.APISandboxScenarioInput) { in.Requests[0].Method = "POST" }},
		{"missing assertion", func(in *api.APISandboxScenarioInput) { in.Requests[0].Assertions = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := valid
			webhook := *in.StripeWebhook
			in.StripeWebhook, in.Requests = &webhook, append([]api.APISandboxExampleRequest(nil), valid.Requests...)
			tc.change(&in)
			if err := ValidateScenario(&in); err == nil {
				t.Fatal("invalid webhook fixture was accepted")
			}
		})
	}
}
