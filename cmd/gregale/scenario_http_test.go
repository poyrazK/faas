package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeTestHTTPWorkflowCapturesAndChecksAcrossPhases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/exports":
			if request.Header.Get("Authorization") != "Bearer buyer-key" || request.Header.Get("Content-Type") != "application/json" {
				t.Errorf("submit headers = %v", request.Header)
			}
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload["run"] != "run-123" {
				t.Errorf("submit payload = (%v, %v)", payload, err)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"export":{"id":"a/b","owner":"buyer"},"invocation":"inv-123"}`))
		case request.Method == http.MethodGet && request.URL.EscapedPath() == "/exports/a%2Fb":
			if request.Header.Get("Authorization") != "Bearer other-key" {
				t.Errorf("read authorization = %q", request.Header.Get("Authorization"))
			}
			w.WriteHeader(http.StatusForbidden)
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	requests := []testHTTPRequest{{
		Name: "submit", As: "buyer", Method: "POST", Path: "/exports", JSON: map[string]any{"run": "${run.id}"},
		Expect:  testHTTPExpect{Status: 202, ContentType: "application/json", JSON: map[string]any{"/export/owner": "buyer"}},
		Capture: map[string]string{"export_id": "/export/id", "worker_invocation_id": "/invocation"},
	}}
	checks := []testHTTPRequest{{Name: "forbidden-read", As: "other", Method: "GET", Path: "/exports/${steps.submit.export_id}", Expect: testHTTPExpect{Status: 403}}}
	scenario := testScenario{Consumers: []testConsumer{{Name: "buyer"}, {Name: "other"}}, Requests: requests, Checks: checks,
		WaitFor: testWaitFor{Invocations: []testInvocationOutput{{Service: "worker", TriggerKey: "worker_invocation_id"}}}}
	if err := validateTestHTTPRequests(scenario); err != nil {
		t.Fatal(err)
	}
	captures := map[string]string{}
	env := []string{"GREGALE_TEST_CONSUMER_BUYER_KEY=buyer-key", "GREGALE_TEST_CONSUMER_OTHER_KEY=other-key"}
	before, err := runTestHTTPRequests(context.Background(), server.URL, "run-123", env, requests, captures)
	if err != nil || len(before) != 1 || !before[0].Passed || before[0].Status != 202 || captures["worker_invocation_id"] != "inv-123" {
		t.Fatalf("before wait = (%+v, %+v, %v)", before, captures, err)
	}
	after, err := runTestHTTPRequests(context.Background(), server.URL, "run-123", env, checks, captures)
	if err != nil || len(after) != 1 || !after[0].Passed || after[0].Status != 403 {
		t.Fatalf("after wait = (%+v, %v)", after, err)
	}
	encoded, err := json.Marshal(append(before, after...))
	if err != nil || strings.Contains(string(encoded), "buyer-key") || strings.Contains(string(encoded), "other-key") || strings.Contains(string(encoded), "a/b") {
		t.Fatalf("request evidence exposed a credential or captured value: %s (%v)", encoded, err)
	}
}

func TestNativeTestHTTPWorkflowRejectsUnexpectedStatusAndRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Location", "https://example.com/escape")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	steps := []testHTTPRequest{{Name: "redirect", Method: "GET", Path: "/start", Expect: testHTTPExpect{Status: 200}}}
	evidence, err := runTestHTTPRequests(context.Background(), server.URL, "run-123", nil, steps, map[string]string{})
	if err == nil || len(evidence) != 1 || evidence[0].Status != 302 || evidence[0].Passed {
		t.Fatalf("redirect = (%+v, %v)", evidence, err)
	}
}

func TestNativeTestHTTPManifestValidationRejectsUnsafeOrUnknownReferences(t *testing.T) {
	base := testScenario{Requests: []testHTTPRequest{{Name: "first", Method: "GET", Path: "/health", Expect: testHTTPExpect{Status: 200}}}}
	cases := []struct {
		name string
		edit func(*testScenario)
	}{
		{"absolute URL", func(s *testScenario) { s.Requests[0].Path = "https://elsewhere.example/" }},
		{"unknown capture", func(s *testScenario) { s.Requests[0].Path = "/${steps.missing.id}" }},
		{"duplicate step", func(s *testScenario) { s.Checks = append(s.Checks, s.Requests[0]) }},
		{"bad pointer", func(s *testScenario) { s.Requests[0].Capture = map[string]string{"id": "id"} }},
		{"unknown consumer", func(s *testScenario) { s.Requests[0].As = "missing" }},
		{"missing status", func(s *testScenario) { s.Requests[0].Expect.Status = 0 }},
		{"external redirect host", func(s *testScenario) { s.Requests[0].Path = "//elsewhere.example/" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scenario := base
			scenario.Requests = append([]testHTTPRequest(nil), base.Requests...)
			tc.edit(&scenario)
			if err := validateTestHTTPRequests(scenario); err == nil {
				t.Fatal("invalid HTTP step was accepted")
			}
		})
	}
}

func TestOpenAPITestScaffoldOnlyIncludesPublicRunnableGETs(t *testing.T) {
	spec := []byte(`openapi: 3.1.0
info: {title: Demo, version: '1'}
security: [{bearer: []}]
paths:
  /health:
    get:
      security: []
      responses:
        '200':
          description: OK
          content: {application/json: {schema: {type: object}}}
  /private:
    get:
      responses: {'200': {description: OK}}
  /items/{id}:
    get:
      security: []
      responses: {'200': {description: OK}}
  /export:
    post:
      security: []
      responses: {'202': {description: Accepted}}
  /search:
    get:
      security: []
      parameters: [{name: q, in: query, required: true, schema: {type: string}}]
      responses: {'200': {description: OK}}
`)
	steps, skipped, err := scaffoldTestHTTPRequests(spec)
	if err != nil || len(steps) != 1 || skipped != 4 || steps[0].Path != "/health" || steps[0].Expect.Status != 200 || steps[0].Expect.ContentType != "application/json" {
		t.Fatalf("scaffold = (%+v, %d, %v)", steps, skipped, err)
	}
	dir := t.TempDir()
	specPath := filepath.Join(dir, "openapi.yaml")
	manifestPath := filepath.Join(dir, "gregale-test.yaml")
	if err := os.WriteFile(specPath, spec, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdTestInit([]string{"--from", specPath, "--project", "demo-api", "--output", manifestPath}); code != 0 {
		t.Fatalf("test init exit = %d", code)
	}
	scenarios, _, err := readTestManifest(manifestPath)
	if err != nil || len(scenarios["api-smoke"].Requests) != 1 || len(scenarios["api-smoke"].Command) != 0 {
		t.Fatalf("generated manifest = (%+v, %v)", scenarios, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "handler.js"), []byte("exports.handler = async () => ({ statusCode: 200, body: '{}' });\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdTest([]string{"--validate", "--manifest", manifestPath}); code != 0 {
		t.Fatalf("generated manifest failed CLI validation with exit %d", code)
	}
	if code := cmdTestInit([]string{"--from", specPath, "--project", "demo-api", "--output", manifestPath}); code == 0 {
		t.Fatal("test init overwrote an existing manifest")
	}
}
