package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomationCheckedPublish(t *testing.T) {
	for _, mode := range []string{"success", "coverage_success", "human_success", "unchanged", "assertion", "incomplete", "invalid", "issues", "simulation_error", "initial_version", "local_mismatch", "changed_version", "changed_definition", "changed_input", "recheck_error", "conflict", "forbidden", "lost_response", "server_check_error", "policy_coverage"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			successful := mode == "success" || mode == "coverage_success" || mode == "human_success" || mode == "unchanged"
			jsonOutput = mode != "human_success"
			output := captureAutomationStdout(t)
			definitionYAML := automationDefinitionYAML
			if mode == "coverage_success" || mode == "policy_coverage" {
				definitionYAML += "    when: {ref: input.ok, op: exists, value: true}\n"
			}
			definitionPath := writeAutomationFixture(t, definitionYAML)
			suitePath := filepath.Join(t.TempDir(), "scenarios.yaml")
			if err := os.WriteFile(suitePath, []byte("definition: "+definitionPath+"\nscenarios:\n  - name: sample\n    require_complete: false\n    expect:\n      record: {state: mocked}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			scenarios, err := loadAutomationScenarios(suitePath)
			if err != nil {
				t.Fatal(err)
			}
			draft := api.AutomationResponse{Name: "paid-invoice", Version: 7, Draft: scenarios[0].request.Definition}
			draft.Draft.Steps = append([]api.WorkflowStepSpec(nil), draft.Draft.Steps...)
			if mode == "human_success" || mode == "unchanged" {
				published := draft.Draft
				published.Steps = append([]api.WorkflowStepSpec(nil), draft.Draft.Steps...)
				if mode == "human_success" {
					published.Steps[0].Input = json.RawMessage(`{"token":"private-scenario-value"}`)
				}
				draft.Published, draft.PublishedVersion = &published, 5
			}
			if mode == "local_mismatch" {
				scenarios[0].request.Definition.Steps[0].Path = "/other"
			}
			var mu sync.Mutex
			var calls []string
			getCount := 0
			publishKey := ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, r.Method+" "+r.URL.Path)
				if r.Header.Get("Authorization") != "Bearer token" {
					t.Error("missing authorization")
				}
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == "GET" && strings.HasSuffix(r.URL.Path, ":publish-policy"):
					policyMode := "optional"
					if mode == "policy_coverage" {
						policyMode = "coverage"
					}
					_ = json.NewEncoder(w).Encode(api.AutomationPublishPolicy{Mode: policyMode, Version: 9})
				case r.Method == "GET":
					getCount++
					current := draft
					if mode == "initial_version" || (mode == "changed_version" && getCount == 2) {
						current.Version++
					}
					if mode == "changed_input" && getCount == 2 {
						current.Draft.Steps = append([]api.WorkflowStepSpec(nil), draft.Draft.Steps...)
						current.Draft.Steps[0].Input = json.RawMessage(`{"token":"private-scenario-value"}`)
					}
					if mode == "changed_definition" && getCount == 2 {
						current.Draft.Name = "changed"
					}
					if mode == "recheck_error" && getCount == 2 {
						w.WriteHeader(503)
						_, _ = w.Write([]byte(`{}`))
						return
					}
					_ = json.NewEncoder(w).Encode(current)
				case strings.HasSuffix(r.URL.Path, ":simulate"):
					if mode == "human_success" && !strings.Contains(output.String(), "/steps/record/input") {
						t.Error("diff was not shown before simulation")
					}
					var request api.SimulateAutomationRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || !reflect.DeepEqual(request.Definition, draft.Draft) {
						t.Errorf("simulation did not use saved draft: %+v %v", request, err)
					}
					if mode == "simulation_error" {
						w.WriteHeader(503)
						_, _ = w.Write([]byte(`{}`))
						return
					}
					response := api.SimulateAutomationResponse{DefinitionValid: mode != "invalid", Complete: mode != "incomplete", Issues: []string{}, Warnings: []string{}, Trace: []api.AutomationSimulationStep{{StepName: "record", Kind: "path", State: "mocked", Output: json.RawMessage(`{"secret":"private-scenario-value"}`)}}}
					if mode == "coverage_success" || mode == "policy_coverage" {
						yes := true
						response.Trace[0].WhenMatched = &yes
					}
					if mode == "assertion" {
						response.Trace[0].State = "skipped"
					}
					if mode == "issues" {
						response.Issues = []string{"private-scenario-value"}
					}
					_ = json.NewEncoder(w).Encode(response)
				case strings.HasSuffix(r.URL.Path, "/publish-check"):
					if mode == "server_check_error" {
						w.WriteHeader(422)
						_, _ = w.Write([]byte(`{"detail":"private-scenario-value"}`))
						return
					}
					var request api.CheckAutomationPublicationRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ExpectedVersion != 7 || len(request.Scenarios) != 1 || len(request.Scenarios[0].Expectations) != 1 {
						t.Errorf("invalid server check request: %+v %v", request, err)
					}
					checked := automationCheckedPublishReport{CheckedVersion: 7, DefinitionHash: fmt.Sprintf("%x", sha256.Sum256(mustJSONForCheck(draft.Draft))), Checks: automationCheckReport{Scenarios: []automationScenarioResult{{Name: "sample", Passed: true, DefinitionValid: true, Complete: true}}}}
					serverEvidence := automationPublicationEvidence(checked, time.Now())
					serverEvidence.ServerVerified = true
					_ = json.NewEncoder(w).Encode(api.CheckAutomationPublicationResponse{Receipt: "server-receipt", ExpiresAt: time.Now().Add(time.Minute), Evidence: *serverEvidence})
				case strings.HasSuffix(r.URL.Path, "/publish"):
					key := r.Header.Get("Idempotency-Key")
					if key == "" || (publishKey != "" && key != publishKey) {
						t.Error("publication replay changed or omitted the idempotency key")
					}
					publishKey = key
					var request api.PublishAutomationRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ExpectedVersion != 7 || !request.TakeOverManifest || request.CheckReceipt != "server-receipt" {
						t.Errorf("publish changed version or takeover: %+v %v", request, err)
					}
					if request.CheckEvidence == nil || request.CheckEvidence.CheckedVersion != 7 || len(request.CheckEvidence.Scenarios) != 1 || !request.CheckEvidence.Scenarios[0].Passed || request.CheckEvidence.Scenarios[0].Name != "sample" || request.CheckEvidence.CheckedAt.IsZero() {
						t.Errorf("missing safe check evidence: %+v", request.CheckEvidence)
					}
					evidenceJSON, _ := json.Marshal(request.CheckEvidence)
					if strings.Contains(string(evidenceJSON), "private-scenario-value") {
						t.Error("sample payload escaped into evidence")
					}
					if mode == "lost_response" {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
						return
					}
					if mode == "conflict" || mode == "forbidden" {
						status := 409
						code := "version_conflict"
						if mode == "forbidden" {
							status, code = 403, "forbidden"
						}
						w.WriteHeader(status)
						_ = json.NewEncoder(w).Encode(api.Problem{Status: status, Code: code, Title: "Rejected", Detail: "private-scenario-value"})
						return
					}
					published := draft
					published.Version, published.PublishedVersion = 8, 8
					_ = json.NewEncoder(w).Encode(published)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			client := api.NewClient(server.URL, "token")
			client.HTTPClient().Timeout = time.Second
			var code int
			if successful {
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", "token")
				code = cmdAutomationsPublish([]string{"--app", "billing", "--name", "paid-invoice", "--expected-version", "7", "--take-over-manifest", "--scenarios", suitePath})
			} else {
				code = publishAutomationWithScenarios(client, "billing", "paid-invoice", 7, true, scenarios)
			}
			wantExit := 1
			if successful {
				wantExit = 0
			}

			if code != wantExit {
				t.Fatalf("exit=%d output=%s", code, output.String())
			}
			mu.Lock()
			observed := append([]string(nil), calls...)
			mu.Unlock()
			base := "/v1/apps/billing/automations/paid-invoice"
			wantCalls := []string{"GET " + base}
			if mode != "initial_version" && mode != "local_mismatch" {
				wantCalls = append(wantCalls, "GET /v1/apps/billing/automations:publish-policy", "POST /v1/apps/billing/automations:simulate")
				if successful || mode == "changed_version" || mode == "changed_definition" || mode == "changed_input" || mode == "recheck_error" || mode == "conflict" || mode == "forbidden" || mode == "lost_response" || mode == "server_check_error" {
					wantCalls = append(wantCalls, "GET "+base)
					if successful || mode == "conflict" || mode == "forbidden" || mode == "lost_response" || mode == "server_check_error" {
						wantCalls = append(wantCalls, "POST "+base+"/publish-check")
						if mode != "server_check_error" {
							wantCalls = append(wantCalls, "POST "+base+"/publish")
						}
					}
				}
			}
			if mode == "lost_response" && len(observed) == 7 {
				wantCalls = append(wantCalls, wantCalls[5])
			}
			if !reflect.DeepEqual(observed, wantCalls) {
				t.Fatalf("calls=%v want=%v", observed, wantCalls)
			}
			if strings.Contains(output.String(), "private-scenario-value") {
				t.Fatal("report leaked sample or service detail")
			}
			if len(wantCalls) > 1 && jsonOutput {
				var report automationCheckedPublishReport
				if err := json.Unmarshal(output.Bytes(), &report); err != nil {
					t.Fatal(err)
				}
				if mode == "policy_coverage" && (report.Checks.Coverage == nil || !report.Checks.Coverage.Required || report.Checks.Coverage.Passed || !strings.Contains(report.Error, "coverage gate failed")) {
					t.Fatalf("app policy not enforced: %+v", report)
				}
				if report.CheckedVersion != 7 || len(report.DefinitionHash) != 64 || report.Published != successful || report.PublicationAttempted != (successful || mode == "conflict" || mode == "forbidden" || mode == "lost_response") {
					t.Fatalf("report=%+v", report)
				}
				if report.Diff.Name != draft.Name || report.Diff.DraftVersion != 7 || report.Diff.FirstPublication != (draft.Published == nil) || report.Diff.Changed != (mode != "unchanged") {
					t.Fatalf("diff=%+v", report.Diff)
				}
				if mode == "coverage_success" && (len(report.Checks.CoverageHints) != 1 || report.Checks.CoverageHints[0].Code != "guard_skip_missing") {
					t.Fatalf("publication did not retain advisory coverage: %+v", report.Checks)
				}
				wantOutcome := "not_attempted"
				if successful {
					wantOutcome = "confirmed"
				}
				if mode == "conflict" || mode == "forbidden" {
					wantOutcome = "rejected"
				}
				if mode == "lost_response" {
					wantOutcome = "unknown"
				}
				if report.PublicationOutcome != wantOutcome {
					t.Fatalf("outcome=%s want=%s", report.PublicationOutcome, wantOutcome)
				}
				if mode == "conflict" && (report.PublicationHTTPStatus != 409 || report.PublicationErrorCode != "version_conflict") {
					t.Fatalf("conflict details=%+v", report)
				}
			}
		})
	}
}

func TestCheckedPublishRejectsEmptyScenarioPath(t *testing.T) {
	resetJSONOut(t)
	fake := authedFakeAPI(t, `{}`, 200)
	if code := cmdAutomationsPublish([]string{"--app", "billing", "--name", "paid-invoice", "--expected-version", "7", "--scenarios", ""}); code != 1 || fake.sawMethod != "" {
		t.Fatalf("exit=%d request=%s", code, fake.sawMethod)
	}
}

func mustJSONForCheck(value any) []byte { raw, _ := json.Marshal(value); return raw }
