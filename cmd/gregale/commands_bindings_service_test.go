package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func servicePolicyFile(t *testing.T, body string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(name, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestServiceBindingConfigurationDecode(t *testing.T) {
	for _, body := range []string{
		`{"service_binding_targets":[" Billing ","billing"],"service_binding_policy":"declared"}`,
		`{"allowed_service_callers":null,"allowed_service_call_scopes":null}`,
		`{"service_binding_targets":[],"allowed_service_callers":[],"allowed_service_call_scopes":{}}`,
		`{"allowed_service_call_scopes":{"Frontend":{"methods":["get"],"path_prefixes":["/notes"]}}}`,
	} {
		config, err := decodeServiceBindingConfiguration([]byte(body), "hello")
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		raw, _ := json.Marshal(config)
		if strings.Contains(string(raw), "Frontend") || strings.Contains(string(raw), " Billing ") {
			t.Fatalf("not canonical: %s", raw)
		}
	}
	for _, body := range []string{
		`{"Allowed_service_callers":[]}`, `null`, `{}`, `[]`, `{} {}`, `{"ram_mb":512}`, `{"service_binding_targets":null}`, `{"service_binding_policy":""}`,
		`{"service_binding_policy":"anything"}`, `{"service_binding_targets":["hello"]}`, `{"service_binding_targets":["bad/name"]}`,
		`{"allowed_service_callers":"frontend"}`, `{"allowed_service_callers":[1]}`, `{"allowed_service_call_scopes":[]}`,
		`{"allowed_service_callers":[],"allowed_service_callers":null}`,
		`{"allowed_service_call_scopes":{"frontend":{"methods":["GET"],"path_prefixes":["/"],"typo":true}}}`,
		`{"allowed_service_call_scopes":{"frontend":{"methods":[],"path_prefixes":["/"]}}}`,
		`{"allowed_service_call_scopes":{"frontend":{"methods":["GET"],"methods":["POST"],"path_prefixes":["/"]}}}`,
	} {
		if _, err := decodeServiceBindingConfiguration([]byte(body), "hello"); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func TestServiceBindingConfigurationCLI(t *testing.T) {
	for _, body := range []string{
		`{"service_binding_targets":["billing"],"service_binding_policy":"declared"}`,
		`{"allowed_service_callers":null,"allowed_service_call_scopes":null}`,
		`{"allowed_service_callers":[],"allowed_service_call_scopes":{},"service_binding_targets":[]}`,
		`{"allowed_service_callers":["frontend"],"allowed_service_call_scopes":{"frontend":{"methods":["GET"],"path_prefixes":["/notes"]}}}`,
	} {
		t.Run(body, func(t *testing.T) {
			resetJSONOut(t)
			policyFile := servicePolicyFile(t, body)
			current := api.AppResponse{Slug: "hello", ServiceBindingPolicy: api.ServiceBindingPolicyAccount, ServiceBindings: []api.AppServiceBinding{{Binding: "billing", Service: "billing"}}}
			calls, patches := 0, 0
			var got map[string]json.RawMessage
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/apps/hello" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				if r.Method == http.MethodPatch {
					patches++
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Error(err)
					}
					var req api.UpdateAppRequest
					raw, _ := json.Marshal(got)
					if err := json.Unmarshal(raw, &req); err != nil {
						t.Error(err)
					}
					if req.ServiceBindingTargets != nil {
						current.ServiceBindings = api.ServiceBindingsForTargets(*req.ServiceBindingTargets)
					}
					if req.ServiceBindingPolicy != nil {
						current.ServiceBindingPolicy = *req.ServiceBindingPolicy
					}
					if len(req.AllowedServiceCallers) > 0 {
						if err := json.Unmarshal(req.AllowedServiceCallers, &current.AllowedServiceCallers); err != nil {
							t.Error(err)
						}
					}
					if len(req.AllowedServiceCallScopes) > 0 {
						if err := json.Unmarshal(req.AllowedServiceCallScopes, &current.AllowedServiceCallScopes); err != nil {
							t.Error(err)
						}
					}
				} else if r.Method != http.MethodGet {
					t.Errorf("unexpected method %s", r.Method)
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
			args := []string{"bindings", "service", "configure", "hello", "--file", policyFile, "--json"}
			if code := run(append(append([]string{}, args...), "--dry-run")); code != 0 || patches != 0 || calls != 1 {
				t.Fatalf("preview code=%d calls=%d patches=%d", code, calls, patches)
			}
			var preview serviceBindingConfigurationReceipt
			if err := json.Unmarshal(output.Bytes(), &preview); err != nil {
				t.Fatal(err)
			}
			if !preview.DryRun || len(preview.Changes) == 0 {
				t.Fatalf("preview=%+v", preview)
			}
			output.Reset()
			if code := run(args); code != 0 || patches != 1 || calls != 3 {
				t.Fatalf("apply code=%d calls=%d patches=%d", code, calls, patches)
			}
			var requested map[string]json.RawMessage
			if err := json.Unmarshal([]byte(body), &requested); err != nil {
				t.Fatal(err)
			}
			for key, value := range requested {
				var want, actual any
				if err := json.Unmarshal(value, &want); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(got[key], &actual); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(want, actual) {
					t.Fatalf("%s wire=%s want=%s", key, got[key], value)
				}
			}
			for _, key := range []string{"allowed_service_callers", "allowed_service_call_scopes", "service_binding_targets", "service_binding_policy"} {
				if _, explicit := requested[key]; !explicit {
					if _, exists := got[key]; exists {
						t.Fatalf("unsolicited %s", key)
					}
				}
			}
			var saved serviceBindingConfigurationReceipt
			if err := json.Unmarshal(output.Bytes(), &saved); err != nil {
				t.Fatal(err)
			}
			if saved.DryRun || len(saved.Changes) != len(requested) {
				t.Fatalf("saved=%+v", saved)
			}
		})
	}
}

func TestServiceBindingConfigurationInvalidBeforeNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "unexpected", 500) }))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	for _, body := range []string{`{}`, `{"allowed_service_callers":[],"typo":true}`, `{"service_binding_targets":["hello"]}`, strings.Repeat(" ", api.SourceManifestMaxBytes+1)} {
		if code := run([]string{"bindings", "service", "configure", "hello", "--file", servicePolicyFile(t, body)}); code == 0 {
			t.Errorf("accepted invalid policy")
		}
	}
	if calls != 0 {
		t.Fatalf("made %d requests", calls)
	}
}

func TestServiceBindingConfigurationHelp(t *testing.T) {
	command, ok := lookupCliCommand("bindings")
	if !ok {
		t.Fatal("missing bindings")
	}
	var reference bytes.Buffer
	renderMarkdownReference(&reference, []cliCommand{command})
	for _, text := range []string{"bindings service configure", "--file", "--dry-run"} {
		if !strings.Contains(reference.String(), text) {
			t.Errorf("reference missing %s", text)
		}
	}
}

func TestServiceBindingReliabilityConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, body      string
		invalid         bool
		wantReliability map[string]api.ServiceReliabilityPolicy
		wantTransport   api.ServiceBindingTransport
		implicit        bool
	}{
		{name: "replace existing", body: `{"service_reliability":{" Billing ":{"timeout_ms":250,"max_attempts":2,"min_remaining_ms":50,"retry_budget_percent":10}}}`, wantReliability: map[string]api.ServiceReliabilityPolicy{"billing": {TimeoutMS: 250, MaxAttempts: 2, MinRemainingMS: 50, RetryBudgetPercent: 10}}},
		{name: "same request targets", body: `{"service_binding_targets":["new-service"],"service_reliability":{"new-service":{"timeout_ms":400}}}`, wantReliability: map[string]api.ServiceReliabilityPolicy{"new-service": {TimeoutMS: 400}}},
		{name: "null reset", body: `{"service_reliability":null}`},
		{name: "empty reset", body: `{"service_reliability":{}}`},
		{name: "prune removed target", body: `{"service_binding_targets":["billing"]}`, wantReliability: map[string]api.ServiceReliabilityPolicy{"billing": {TimeoutMS: 100}}, implicit: true},
		{name: "prune all targets", body: `{"service_binding_targets":[]}`, implicit: true},
		{name: "https canonical", body: `{"service_binding_transport":" HTTPS "}`, wantTransport: api.ServiceBindingTransportHTTPS, wantReliability: map[string]api.ServiceReliabilityPolicy{"billing": {TimeoutMS: 100}, "identity": {TimeoutMS: 200}}},
		{name: "http selection", body: `{"service_binding_transport":"http"}`, wantTransport: api.ServiceBindingTransportHTTP, wantReliability: map[string]api.ServiceReliabilityPolicy{"billing": {TimeoutMS: 100}, "identity": {TimeoutMS: 200}}},
		{name: "explicit unsafe retry opt in", body: `{"service_reliability":{"billing":{"max_attempts":2,"allow_non_idempotent":true}}}`, wantReliability: map[string]api.ServiceReliabilityPolicy{"billing": {MaxAttempts: 2, AllowNonIdempotent: true}}},
		{name: "undeclared", body: `{"service_reliability":{"unknown":{"timeout_ms":100}}}`, invalid: true},
		{name: "removed target override", body: `{"service_binding_targets":["billing"],"service_reliability":{"identity":{"timeout_ms":100}}}`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetJSONOut(t)
			before := api.AppResponse{Slug: "hello", ServiceBindings: api.ServiceBindingsForTargets([]string{"billing", "identity"}), ServiceReliability: map[string]api.ServiceReliabilityPolicy{"billing": {TimeoutMS: 100}, "identity": {TimeoutMS: 200}}}
			calls, patches := 0, 0
			var got api.UpdateAppRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method == http.MethodGet {
					writeJSONTest(w, before)
					return
				}
				if r.Method != http.MethodPatch {
					t.Errorf("unexpected %s", r.Method)
				}
				patches++
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				after := before
				if got.ServiceBindingTargets != nil {
					after.ServiceBindings = api.ServiceBindingsForTargets(*got.ServiceBindingTargets)
				}
				if got.ServiceBindingTransport != nil {
					after.ServiceBindingTransport = *got.ServiceBindingTransport
				}
				// Independently model server replacement and implicit pruning.
				if len(got.ServiceReliability) > 0 {
					after.ServiceReliability = nil
					if err := json.Unmarshal(got.ServiceReliability, &after.ServiceReliability); err != nil {
						t.Error(err)
					}
					if len(after.ServiceReliability) == 0 {
						after.ServiceReliability = nil
					}
				} else if got.ServiceBindingTargets != nil {
					after.ServiceReliability = map[string]api.ServiceReliabilityPolicy{}
					for _, binding := range after.ServiceBindings {
						if policy, ok := before.ServiceReliability[binding.Service]; ok {
							after.ServiceReliability[binding.Service] = policy
						}
					}
					if len(after.ServiceReliability) == 0 {
						after.ServiceReliability = nil
					}
				}
				if !reflect.DeepEqual(after.ServiceReliability, tc.wantReliability) {
					t.Errorf("server reliability=%v want=%v", after.ServiceReliability, tc.wantReliability)
				}
				writeJSONTest(w, after)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			var output bytes.Buffer
			old := osStdout
			osStdout = &output
			t.Cleanup(func() { osStdout = old })
			args := []string{"bindings", "service", "configure", "hello", "--file", servicePolicyFile(t, tc.body), "--json"}
			previewCode := run(append(append([]string{}, args...), "--dry-run"))
			if patches != 0 || calls != 1 {
				t.Fatalf("preview calls=%d patches=%d", calls, patches)
			}
			if tc.invalid {
				if previewCode == 0 {
					t.Fatal("invalid preview accepted")
				}
				if run(args) == 0 || patches != 0 || calls != 2 {
					t.Fatal("invalid update mutated state")
				}
				return
			}
			if previewCode != 0 {
				t.Fatalf("preview code=%d", previewCode)
			}
			var preview serviceBindingConfigurationReceipt
			if err := json.Unmarshal(output.Bytes(), &preview); err != nil {
				t.Fatal(err)
			}
			if tc.implicit {
				change, exists := preview.Changes["service_reliability"]
				if !exists {
					t.Fatal("implicit pruning missing")
				}
				raw, _ := json.Marshal(change.After)
				var policies map[string]api.ServiceReliabilityPolicy
				if err := json.Unmarshal(raw, &policies); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(policies, tc.wantReliability) {
					t.Fatalf("preview pruning=%v want=%v", policies, tc.wantReliability)
				}
			}
			output.Reset()
			if code := run(args); code != 0 || patches != 1 || calls != 3 {
				t.Fatalf("apply code=%d calls=%d patches=%d", code, calls, patches)
			}
			if tc.implicit && len(got.ServiceReliability) != 0 {
				t.Fatal("implicit pruning became explicit replacement")
			}
			if tc.wantTransport != "" && (got.ServiceBindingTransport == nil || *got.ServiceBindingTransport != tc.wantTransport) {
				t.Fatalf("transport=%v", got.ServiceBindingTransport)
			}
			if tc.wantTransport != "" && len(got.ServiceReliability) != 0 {
				t.Fatal("transport update overwrote reliability")
			}
			var saved serviceBindingConfigurationReceipt
			if err := json.Unmarshal(output.Bytes(), &saved); err != nil {
				t.Fatal(err)
			}
			if tc.implicit {
				if _, exists := saved.Changes["service_reliability"]; !exists {
					t.Fatal("saved pruning missing")
				}
			}
			if len(before.ServiceReliability) != 2 {
				t.Fatal("preview mutated prior reliability map")
			}
		})
	}
}

func TestServiceBindingReliabilityInvalidBeforeNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "unexpected", 500) }))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	for _, body := range []string{
		`{"service_binding_transport":""}`, `{"service_binding_transport":"ftp"}`, `{"service_binding_transport":null}`,
		`{"service_reliability":[]}`, `{"service_reliability":{"billing":{"timeout_ms":-1}}}`,
		`{"service_reliability":{"billing":{"max_attempts":-1}}}`, `{"service_reliability":{"billing":{"retry_budget_percent":101}}}`,
		`{"service_reliability":{"billing":{"timeout_ms":10,"min_remaining_ms":11}}}`,
		`{"service_reliability":{"billing":{"allow_non_idempotent":"true"}}}`,
		`{"service_reliability":{"billing":{"unknown":1}}}`, `{"service_reliability":{"billing":{}," Billing ":{}}}`,
		`{"service_reliability":{"billing":{"max_attempts":1,"max_attempts":2}}}`,
	} {
		if code := run([]string{"bindings", "service", "configure", "hello", "--file", servicePolicyFile(t, body)}); code == 0 {
			t.Errorf("accepted %s", body)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid configuration made %d requests", calls)
	}
}
