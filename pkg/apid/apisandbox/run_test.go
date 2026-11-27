package apisandbox

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAPISandboxRunStepChecksRevisionAndJSONWithoutKeepingBodies(t *testing.T) {
	const response = `{"items":[{"a/b~c":9007199254740993.0,"value":null}],"secret":"PRIVATE RESPONSE"}`
	for _, test := range []struct {
		name, revision, body, pointer, equals, failure string
		status                                         int
	}{
		{"exact large number", "deployed", response, "/items/0/a~1b~0c", "9007199254740993", "", 201},
		{"missing isn't null", "deployed", response, "/items/0/missing", "null", "json_assertion_failed", 201},
		{"JSON null", "deployed", response, "/items/0/value", "null", "", 201},
		{"wrong value", "deployed", response, "/items/0/a~1b~0c", "9007199254740994", "json_assertion_failed", 201},
		{"wrong revision", "other", response, "/items/0/value", "null", "deployment_mismatch", 201},
		{"redirect", "deployed", response, "/items/0/value", "null", "unexpected_http_status", 302},
		{"malformed JSON", "deployed", response + "{}", "/items/0/value", "null", "invalid_json_response", 201},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodPost || r.URL.Host != "sandbox.example.test" || r.URL.RawQuery != "limit=1" ||
					r.Header.Get("Authorization") != "Bearer run-token" {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL)
				}
				headers := make(http.Header)
				headers.Set(api.RevisionHeader, test.revision)
				headers.Set(api.RequestIDHeader, r.Header.Get(api.RequestIDHeader))
				headers.Set("Location", "https://elsewhere.example/")
				return &http.Response{StatusCode: test.status, Header: headers, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			step := api.APISandboxExampleRequest{
				Title: "Create fixture", Method: "POST", Path: "/items?limit=1", ExpectedStatus: 201,
				Body:       json.RawMessage(`{"value":"PRIVATE REQUEST"}`),
				Assertions: []api.APISandboxJSONAssertion{{Pointer: test.pointer, Equals: json.RawMessage(test.equals)}},
			}
			result := RunStep(t.Context(), client, "https://sandbox.example.test", "deployed", "run-token", 1, step)
			if result.FailureCode != test.failure || calls != 1 || result.RequestID == "" || result.HTTPStatus != test.status {
				t.Fatalf("result=%+v calls=%d", result, calls)
			}
			if test.failure == "json_assertion_failed" && (result.FailedAssertion != 1 || result.FailedPointer != test.pointer) {
				t.Fatalf("failed assertion not identified: %+v", result)
			}
			data, err := json.Marshal(result)
			if err != nil || strings.Contains(string(data), "PRIVATE") || strings.Contains(string(data), "run-token") {
				t.Fatalf("step retained private data")
			}
		})
	}
}

func TestAPISandboxScenarioRejectsInvalidAssertions(t *testing.T) {
	for _, assertion := range []api.APISandboxJSONAssertion{
		{Pointer: "field", Equals: json.RawMessage(`true`)},
		{Pointer: "/field~2", Equals: json.RawMessage(`true`)},
		{Pointer: "/field", Equals: json.RawMessage(`{"nested":true}`)},
		{Pointer: "/field"},
	} {
		if err := validateAssertions(http.MethodGet, []api.APISandboxJSONAssertion{assertion}); err == nil {
			t.Errorf("accepted invalid assertion %+v", assertion)
		}
	}
}

func TestAPISandboxWebhookStateTimeoutKeepsLastAssertionEvidence(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set(api.RevisionHeader, "pinned")
		header.Set(api.RequestIDHeader, r.Header.Get(api.RequestIDHeader))
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(`{"active":false}`))}, nil
	})}
	step := api.APISandboxExampleRequest{
		Title: "Check state", Method: http.MethodGet, Path: "/subscription?fixture=private", ExpectedStatus: 200,
		Assertions: []api.APISandboxJSONAssertion{{Pointer: "/active", Equals: json.RawMessage(`true`)}},
	}
	result := WaitForState(t.Context(), client, "https://sandbox.example.test", "pinned", "fixture-key", 4, step, 250*time.Millisecond)
	if result.FailureCode != "webhook_state_timeout" || result.FailedPointer != "/active" ||
		result.HTTPStatus != http.StatusOK || result.Path != "/subscription" || result.RequestID == "" {
		t.Fatalf("wrong timeout evidence: %+v", result)
	}
}
