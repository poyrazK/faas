package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestServiceBindingPolicyExportCheck(t *testing.T) {
	resetJSONOut(t)
	current := api.AppResponse{Slug: "hello", ServiceBindings: []api.AppServiceBinding{{Binding: "billing", Service: "billing"}}}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/hello" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		writeJSONTest(w, current)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	var output bytes.Buffer
	old := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = old })
	path := filepath.Join(t.TempDir(), "export.json")
	if code := run([]string{"bindings", "service", "export", "hello", "--out", path, "--json"}); code != 0 {
		t.Fatalf("export=%d", code)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 6 || string(fields["allowed_service_callers"]) != "null" || string(fields["allowed_service_call_scopes"]) != "null" || string(fields["service_reliability"]) != "{}" {
		t.Fatalf("export=%s", raw)
	}
	if _, err := decodeServiceBindingConfiguration(raw, "hello"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%v", info.Mode())
	}
	if code := run([]string{"bindings", "service", "export", "hello", "--out", path}); code != 2 {
		t.Fatalf("overwrite=%d", code)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, unchanged) {
		t.Fatal("export overwritten")
	}
	output.Reset()
	if code := run([]string{"bindings", "service", "check", "hello", "--file", path, "--json"}); code != 0 {
		t.Fatalf("roundtrip=%d output=%s", code, output.String())
	}
	empty := []string{}
	current.AllowedServiceCallers = &empty
	output.Reset()
	if code := run([]string{"bindings", "service", "check", "hello", "--file", path, "--json"}); code != 1 {
		t.Fatalf("drift=%d", code)
	}
	var receipt struct {
		Match   bool                                         `json:"match"`
		Changes map[string]serviceBindingConfigurationChange `json:"changes"`
	}
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Match || len(receipt.Changes) != 1 {
		t.Fatalf("receipt=%+v", receipt)
	}
	// Partial policies ignore other settings and normalize ordering/case.
	callers := []string{"worker", "frontend"}
	current.AllowedServiceCallers = &callers
	current.AllowedServiceCallScopes = &api.ServiceCallerScopes{"frontend": {Methods: []string{"POST", "GET"}, PathPrefixes: []string{"/z", "/a"}}}
	for _, body := range []string{
		`{"allowed_service_callers":["Frontend","worker","frontend"]}`,
		`{"allowed_service_call_scopes":{"frontend":{"methods":["get","POST"],"path_prefixes":["/a","/z"]}}}`,
		`{"service_binding_targets":["billing"],"service_reliability":null}`,
	} {
		policy := servicePolicyFile(t, body)
		output.Reset()
		if code := run([]string{"bindings", "service", "check", "hello", "--file", policy, "--json"}); code != 0 {
			t.Fatalf("%s code=%d output=%s", body, code, output.String())
		}
	}
	previous := calls
	invalid := servicePolicyFile(t, `{"allowed_service_callers":[],"allowed_service_callers":null}`)
	if code := run([]string{"bindings", "service", "check", "hello", "--file", invalid}); code != 2 || calls != previous {
		t.Fatalf("invalid policy touched API: code=%d calls=%d", code, calls)
	}
	output.Reset()
	if code := run([]string{"bindings", "service", "export", "hello", "--json"}); code != 0 {
		t.Fatalf("stdout=%d", code)
	}
	if _, err := decodeServiceBindingConfiguration(output.Bytes(), "hello"); err != nil {
		t.Fatalf("stdout: %v", err)
	}
}

func TestServiceBindingPolicyCheckAPIFailure(t *testing.T) {
	resetJSONOut(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected mutation %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"title":"Forbidden","status":403}`))
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	var stderr bytes.Buffer
	old := osStderr
	osStderr = &stderr
	t.Cleanup(func() { osStderr = old })
	policy := servicePolicyFile(t, `{"service_binding_policy":"declared"}`)
	if code := run([]string{"bindings", "service", "check", "hello", "--file", policy, "--json"}); code != 2 {
		t.Fatalf("API error exit=%d", code)
	}
	var diagnostic struct {
		ExitCode int `json:"exit_code"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &diagnostic); err != nil {
		t.Fatal(err)
	}
	if diagnostic.ExitCode != 2 {
		t.Fatalf("diagnostic exit=%d", diagnostic.ExitCode)
	}
}

func TestServiceBindingPolicyPresentEmptyCollections(t *testing.T) {
	var callers []string
	var scopes api.ServiceCallerScopes
	config, err := liveServicePolicy(api.AppResponse{Slug: "hello", AllowedServiceCallers: &callers, AllowedServiceCallScopes: &scopes})
	if err != nil {
		t.Fatal(err)
	}
	fields, err := canonicalServicePolicy(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(fields["allowed_service_callers"]) != "[]" || string(fields["allowed_service_call_scopes"]) != "{}" {
		t.Fatalf("empty policies became unrestricted: %s", fields)
	}
}
