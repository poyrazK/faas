package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdBindingsVerifyAllUsesInventoryLiveScope(t *testing.T) {
	for _, severity := range []string{"", "warning", "error"} {
		t.Run("read issue "+severity, func(t *testing.T) {
			var created []string
			inventoryCalls := 0
			outboundID := "integration"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/bindings":
					inventoryCalls++
					credentialConfigured := true
					inventory := api.AppBindingInventory{App: "api", VerificationScope: "production", Bindings: []api.AppBindingInventoryItem{
						{Type: api.BindingTypeService, Name: "billing", Binding: "GREGALE_SERVICE_BILLING_URL", Scope: "app"},
						{Type: api.BindingTypePostgres, Name: "primary", Binding: "DATABASE_URL", Scope: "production"},
						{Type: api.BindingTypePostgres, Name: "stage", Binding: "STAGING_URL", Scope: "staging"},
						{Type: api.BindingTypeObjectStorage, Name: "assets", Binding: "ASSETS", Scope: "production"},
						{Type: api.BindingTypeObjectStorage, Name: "assets-stage", Binding: "STAGING_ASSETS", Scope: "staging"},
						{Type: api.BindingTypeOutbound, Name: "payments", Scope: "app", State: "enabled", CredentialConfigured: &credentialConfigured, OutboundProbe: &api.OutboundBindingProbePolicy{Method: "GET", Path: "/health", ExpectedStatus: 200}},
						{Type: api.BindingTypeQueue, Name: "orders", Scope: "app"},
					}}
					if severity != "" {
						inventory.Issues = []api.BindingInventoryIssue{{Type: api.BindingTypePostgres, Severity: severity, Code: "query_failed", Message: "Metadata could not be read."}}
					}
					_ = json.NewEncoder(w).Encode(inventory)
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/outbound-bindings":
					_ = json.NewEncoder(w).Encode(api.OutboundAppBindingList{Items: []api.OutboundAppBinding{{Integration: api.OutboundIntegrationOffer{ID: outboundID, Name: "payments"}}}})
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks":
					var request api.CreateAppTaskRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Command) != 2 {
						t.Errorf("invalid probe request: %+v %v", request, err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					created = append(created, request.Command[1])
					var report any
					if request.Command[0] == api.AppTaskPostgresBindingProbeCommand {
						report = api.PostgresBindingProbeReport{EnvironmentKey: request.Command[1],
							Environment: api.PostgresBindingProbeCheck{Status: "passed"}, Configuration: api.PostgresBindingProbeCheck{Status: "passed"},
							Connection: api.PostgresBindingProbeCheck{Status: "passed"}, Query: api.PostgresBindingProbeCheck{Status: "passed"}}
					} else if request.Command[0] == api.AppTaskServiceBindingProbeCommand {
						report = api.ServiceBindingProbeReport{Service: request.Command[1],
							DNS: api.ServiceBindingProbeCheck{Status: "passed"}, TLS: api.ServiceBindingProbeCheck{Status: "passed"},
							Authorization: api.ServiceBindingProbeCheck{Status: "passed"}, Routing: api.ServiceBindingProbeCheck{Status: "passed"}}
					} else if request.Command[0] == api.AppTaskObjectStorageBindingProbeCommand {
						report = passedObjectStorageCLIReport(request.Command[1])
					} else if request.Command[0] == api.AppTaskOutboundBindingProbeCommand {
						check := api.OutboundBindingProbeCheck{Status: "passed"}
						report = api.OutboundBindingProbeReport{IntegrationID: outboundID, Configuration: check, Identity: check, Gateway: check, Response: check}
					} else {
						t.Errorf("unsupported probe command: %v", request.Command)
					}
					body, _ := json.Marshal(report)
					_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: "task-" + request.Command[1], Status: api.AppTaskStatusSucceeded, StdoutTail: string(body), ExitCode: new(int)})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_test")
			var stdout, stderr bytes.Buffer
			previousOut, previousErr, previousJSON := osStdout, osStderr, jsonOutput
			osStdout, osStderr, jsonOutput = &stdout, &stderr, true
			t.Cleanup(func() { osStdout, osStderr, jsonOutput = previousOut, previousErr, previousJSON })
			wantExit := 0
			if severity == "error" {
				wantExit = 1
			}
			if code := run([]string{"bindings", "verify", "api", "--all"}); code != wantExit {
				t.Fatalf("exit=%d, output=%s %s", code, stdout.String(), stderr.String())
			}
			if inventoryCalls != 1 || !reflect.DeepEqual(created, []string{"ASSETS", outboundID, "DATABASE_URL", "billing"}) {
				t.Fatalf("inventory calls=%d probes=%v", inventoryCalls, created)
			}
			var report serviceBindingProbeBatchReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.Scope != "production" || report.Passed != 4 || report.Checked != 4 || report.Total != 4 {
				t.Fatalf("batch=%+v err=%v", report, err)
			}
			if report.Bindings[0].ObjectStorageReport == nil || report.Bindings[1].OutboundReport == nil || report.Bindings[2].PostgresReport == nil || report.Bindings[3].Report == nil || (severity != "" && len(report.Issues) != 1) {
				t.Fatalf("missing typed reports or issues: %+v", report)
			}
		})
	}
}

func TestCmdBindingsVerifyRejectsUnsafeSuccessfulReports(t *testing.T) {
	for _, kind := range []string{api.BindingTypeService, api.BindingTypePostgres} {
		for _, failure := range []string{"truncated", "wrong binding", "missing binding", "contradictory error", "nonzero exit"} {
			t.Run(kind+" "+failure, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.Method == http.MethodGet {
						if kind == api.BindingTypeService {
							_, _ = w.Write([]byte(`{"slug":"api","service_bindings":[{"service":"billing","binding":"GREGALE_SERVICE_BILLING_URL"}]}`))
						} else {
							_, _ = w.Write([]byte(`{"verification_scope":"production","bindings":[{"type":"postgres","binding":"DATABASE_URL","scope":"production"}]}`))
						}
						return
					}
					identity, reportError := "billing", ""
					if kind == api.BindingTypePostgres {
						identity = "DATABASE_URL"
					}
					if failure == "wrong binding" {
						identity = "another"
					} else if failure == "missing binding" {
						identity = ""
					} else if failure == "contradictory error" {
						reportError = "probe failed"
					}
					var report any = api.ServiceBindingProbeReport{Service: identity, Error: reportError,
						DNS: api.ServiceBindingProbeCheck{Status: "passed"}, TLS: api.ServiceBindingProbeCheck{Status: "passed"},
						Authorization: api.ServiceBindingProbeCheck{Status: "passed"}, Routing: api.ServiceBindingProbeCheck{Status: "passed"}}
					if kind == api.BindingTypePostgres {
						report = api.PostgresBindingProbeReport{EnvironmentKey: identity, Error: reportError,
							Environment: api.PostgresBindingProbeCheck{Status: "passed"}, Configuration: api.PostgresBindingProbeCheck{Status: "passed"},
							Connection: api.PostgresBindingProbeCheck{Status: "passed"}, Query: api.PostgresBindingProbeCheck{Status: "passed"}}
					}
					body, _ := json.Marshal(report)
					exit := 0
					if failure == "nonzero exit" {
						exit = 1
					}
					_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: "task-1", Status: api.AppTaskStatusSucceeded, StdoutTail: string(body), OutputTruncated: failure == "truncated", ExitCode: &exit})
				}))
				defer srv.Close()
				t.Setenv("FAAS_API", srv.URL)
				t.Setenv("FAAS_TOKEN", "fp_live_test")
				args := []string{"bindings", "verify", "api", "billing"}
				if kind == api.BindingTypePostgres {
					args = []string{"bindings", "verify", "api", "--postgres", "DATABASE_URL"}
				}
				if code := run(args); code != 1 {
					t.Fatalf("unsafe report accepted: exit=%d", code)
				}
			})
		}
	}
}

func TestCmdBindingsVerifyAllReportsUnreadableEmptySelection(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/api/bindings" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.AppBindingInventory{App: "api", Bindings: []api.AppBindingInventoryItem{},
			Issues: []api.BindingInventoryIssue{{Type: api.BindingTypePostgres, Severity: "error", Code: "forbidden", Message: "PostgreSQL metadata requires read permission."}}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var stdout, stderr bytes.Buffer
	previousOut, previousErr, previousJSON := osStdout, osStderr, jsonOutput
	osStdout, osStderr, jsonOutput = &stdout, &stderr, true
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = previousOut, previousErr, previousJSON })
	if code := run([]string{"bindings", "verify", "api", "--all"}); code != 1 {
		t.Fatalf("exit=%d", code)
	}
	var report serviceBindingProbeBatchReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.Total != 0 || len(report.Issues) != 1 || report.Issues[0].Code != "forbidden" || calls != 1 {
		t.Fatalf("missing partial result: %+v err=%v output=%s", report, err, stdout.String())
	}
}

func TestCmdBindingsVerifyRejectsDeploymentSelectionChanges(t *testing.T) {
	for _, all := range []bool{false, true} {
		for _, change := range []string{"deployment", "scope"} {
			t.Run(map[bool]string{false: "PostgreSQL", true: "all"}[all]+" "+change, func(t *testing.T) {
				deployment, scope := "deployment-original", "production"
				if change == "deployment" {
					deployment = "deployment-replacement"
				} else {
					scope = "staging"
				}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/bindings" {
						_ = json.NewEncoder(w).Encode(api.AppBindingInventory{App: "api", VerificationDeploymentID: "deployment-original", VerificationScope: "production",
							Bindings: []api.AppBindingInventoryItem{
								{Type: api.BindingTypePostgres, Name: "primary", Binding: "DATABASE_URL", Scope: "production"},
								{Type: api.BindingTypeService, Name: "billing", Binding: "GREGALE_SERVICE_BILLING_URL", Scope: "app"},
							}})
						return
					}
					if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/api/tasks" {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL)
						http.NotFound(w, r)
						return
					}
					var request api.CreateAppTaskRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Command) != 2 {
						t.Errorf("invalid probe request: %+v %v", request, err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					body := `{"environment_key":"DATABASE_URL","environment":{"status":"passed"},"configuration":{"status":"passed"},"connection":{"status":"passed"},"query":{"status":"passed"}}`
					if request.Command[0] == api.AppTaskServiceBindingProbeCommand {
						body = `{"service":"billing","dns":{"status":"passed"},"tls":{"status":"passed"},"authorization":{"status":"passed"},"routing":{"status":"passed"}}`
					}
					_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: "task-1", DeploymentID: deployment, DeploymentScope: scope, Status: api.AppTaskStatusSucceeded, StdoutTail: body})
				}))
				defer srv.Close()
				t.Setenv("FAAS_API", srv.URL)
				t.Setenv("FAAS_TOKEN", "fp_live_test")
				var stdout, stderr bytes.Buffer
				previousOut, previousErr, previousJSON := osStdout, osStderr, jsonOutput
				osStdout, osStderr, jsonOutput = &stdout, &stderr, true
				t.Cleanup(func() { osStdout, osStderr, jsonOutput = previousOut, previousErr, previousJSON })
				args := []string{"bindings", "verify", "api", "--postgres", "DATABASE_URL"}
				if all {
					args = []string{"bindings", "verify", "api", "--all"}
				}
				if code := run(args); code != 1 || !strings.Contains(stdout.String(), "deployment changed") {
					t.Fatalf("changed deployment accepted: exit=%d output=%s %s", code, stdout.String(), stderr.String())
				}
				if all {
					var report serviceBindingProbeBatchReport
					if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.Failed != 2 || report.Passed != 0 {
						t.Fatalf("batch=%+v err=%v", report, err)
					}
					for _, item := range report.Bindings {
						if item.Scope != scope {
							t.Fatalf("scope misreported: %+v", item)
						}
					}
				}
			})
		}
	}
}
