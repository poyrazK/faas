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

func TestCmdBindingsVerifyRunsInBoundAppTaskAndReportsStages(t *testing.T) {
	report := api.ServiceBindingProbeReport{
		Service:       "billing",
		URL:           "https://billing.internal",
		DNS:           api.ServiceBindingProbeCheck{Status: "passed", Detail: "resolved to 1 address(es)"},
		TLS:           api.ServiceBindingProbeCheck{Status: "passed", Detail: "TLS 1.3; certificate verified"},
		Authorization: api.ServiceBindingProbeCheck{Status: "passed"},
		Routing:       api.ServiceBindingProbeCheck{Status: "passed"},
	}
	reportJSON, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	terminal := api.AppTaskResponse{
		ID: "task-1", AppID: "app-1", DeploymentID: "deployment-1", Kind: api.AppTaskKindManual,
		Status: api.AppTaskStatusSucceeded, StdoutTail: string(reportJSON), MaxOutputBytes: 4096,
	}
	var createCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api":
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"api","service_bindings":[{"binding":"GREGALE_SERVICE_BILLING_URL","service":"billing"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks":
			createCalls++
			var request api.CreateAppTaskRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode create task: %v", err)
			}
			if len(request.Command) != 2 || request.Command[0] != api.AppTaskServiceBindingProbeCommand || request.Command[1] != "billing" || request.CommandShell {
				t.Errorf("task command = %+v", request)
			}
			if request.TimeoutSeconds != bindingProbeTaskTimeoutSeconds || request.MaxOutputBytes != 4096 {
				t.Errorf("task limits = %+v", request)
			}
			queued := api.AppTaskResponse{ID: "task-1", Status: api.AppTaskStatusQueued}
			_ = json.NewEncoder(w).Encode(queued)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/tasks/task-1":
			_ = json.NewEncoder(w).Encode(terminal)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	previousJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = previousJSON })
	var out bytes.Buffer
	previousOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })
	if code := run([]string{"bindings", "verify", "api", "billing", "--poll-interval", "1ms", "--wait-timeout", "1s"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if createCalls != 1 {
		t.Fatalf("create task calls = %d, want one", createCalls)
	}
	var got api.ServiceBindingProbeReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode report: %v; output=%s", err, out.String())
	}
	if !got.Passed() || got.App != "api" || got.Service != "billing" || got.TLS.Status != "passed" ||
		got.TaskID != "task-1" || got.DeploymentID != "deployment-1" {
		t.Fatalf("report = %+v", got)
	}
}

func TestCmdBindingsVerifyPostgresRunsSelectedManagedBindingCanary(t *testing.T) {
	report := api.PostgresBindingProbeReport{
		EnvironmentKey: "DATABASE_URL",
		Environment:    api.PostgresBindingProbeCheck{Status: "passed", Detail: "environment variable is present in this deployment"},
		Configuration:  api.PostgresBindingProbeCheck{Status: "passed"},
		Connection:     api.PostgresBindingProbeCheck{Status: "passed"},
		Query:          api.PostgresBindingProbeCheck{Status: "passed"},
	}
	reportJSON, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var createCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api":
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"api"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/bindings":
			_, _ = w.Write([]byte(`{"app":"api","verification_scope":"default","verification_deployment_id":"deployment-1","bindings":[{"type":"postgres","binding":"OTHER_URL","scope":"default"},{"type":"postgres","binding":"DATABASE_URL","scope":"staging"},{"type":"postgres","binding":"DATABASE_URL","scope":"default"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks":
			createCalls++
			var request api.CreateAppTaskRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode create task: %v", err)
			}
			if !reflect.DeepEqual(request.Command, []string{api.AppTaskPostgresBindingProbeCommand, "DATABASE_URL"}) || request.CommandShell {
				t.Errorf("task command = %+v", request)
			}
			if request.TimeoutSeconds != bindingProbeTaskTimeoutSeconds || request.MaxOutputBytes != 4096 {
				t.Errorf("task limits = %+v", request)
			}
			_ = json.NewEncoder(w).Encode(api.AppTaskResponse{
				ID: "task-pg-1", AppID: "app-1", DeploymentID: "deployment-1",
				Kind: api.AppTaskKindManual, Status: api.AppTaskStatusSucceeded, StdoutTail: string(reportJSON),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	previousJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = previousJSON })
	var out bytes.Buffer
	previousOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })

	if code := run([]string{"bindings", "verify", "api", "--postgres", "DATABASE_URL", "--poll-interval", "1ms", "--wait-timeout", "1s"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if createCalls != 1 {
		t.Fatalf("create task calls = %d, want one", createCalls)
	}
	var got api.PostgresBindingProbeReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode report: %v; output=%s", err, out.String())
	}
	if !got.Passed() || got.App != "api" || got.EnvironmentKey != "DATABASE_URL" ||
		got.TaskID != "task-pg-1" || got.DeploymentID != "deployment-1" {
		t.Fatalf("report = %+v", got)
	}
}

func TestCmdBindingsVerifyPostgresRejectsUnboundEnvironmentKeyBeforeTaskAdmission(t *testing.T) {
	var createCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api":
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"api"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/bindings":
			_, _ = w.Write([]byte(`{"app":"api","verification_scope":"default","bindings":[{"type":"postgres","binding":"DATABASE_URL","scope":"staging"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks":
			createCalls++
			http.Error(w, "unexpected task admission", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	if code := run([]string{"bindings", "verify", "api", "--postgres", "DATABASE_URL"}); code == 0 {
		t.Fatal("verification succeeded for an environment key not bound to this app")
	}
	if createCalls != 0 {
		t.Fatalf("created %d canary task(s) for an unbound environment key", createCalls)
	}
}

func TestCmdBindingsVerifyRejectsUndeclaredServiceBeforeTaskAdmission(t *testing.T) {
	var createCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api" {
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"api","service_bindings":[]}`))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks" {
			createCalls++
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	if code := run([]string{"bindings", "verify", "api", "billing"}); code == 0 {
		t.Fatal("undeclared service unexpectedly verified")
	}
	if createCalls != 0 {
		t.Fatalf("created %d task(s) for an undeclared service", createCalls)
	}
}

func TestCmdBindingsVerifyAllAggregatesEveryDeclaredService(t *testing.T) {
	var created []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/bindings":
			_ = json.NewEncoder(w).Encode(api.AppBindingInventory{
				App: "api", VerificationScope: "default",
				Bindings: []api.AppBindingInventoryItem{
					{Binding: "GREGALE_SERVICE_EMAIL_URL", Type: api.BindingTypeService, Name: "email"},
					{Binding: "GREGALE_SERVICE_BILLING_URL", Type: api.BindingTypeService, Name: "billing"},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks":
			var request api.CreateAppTaskRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode create task: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if len(request.Command) != 2 || request.Command[0] != api.AppTaskServiceBindingProbeCommand || request.CommandShell {
				t.Errorf("task command = %+v", request)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			service := request.Command[1]
			created = append(created, service)
			report := api.ServiceBindingProbeReport{
				Service:       service,
				URL:           "https://" + service + ".internal",
				DNS:           api.ServiceBindingProbeCheck{Status: "passed"},
				TLS:           api.ServiceBindingProbeCheck{Status: "passed"},
				Authorization: api.ServiceBindingProbeCheck{Status: "passed"},
				Routing:       api.ServiceBindingProbeCheck{Status: "passed"},
			}
			if service == "email" {
				report.TLS = api.ServiceBindingProbeCheck{Status: "failed", Detail: "certificate was not trusted"}
				report.Authorization = api.ServiceBindingProbeCheck{Status: "not_checked"}
				report.Routing = api.ServiceBindingProbeCheck{Status: "not_checked"}
				report.Error = "TLS certificate verification failed"
			}
			reportJSON, err := json.Marshal(report)
			if err != nil {
				t.Errorf("marshal report: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(api.AppTaskResponse{
				ID: "task-" + service, AppID: "app-1", DeploymentID: "deployment-1",
				Kind: api.AppTaskKindManual, Status: api.AppTaskStatusSucceeded, StdoutTail: string(reportJSON),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	previousJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = previousJSON })
	var out bytes.Buffer
	previousOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })

	if code := run([]string{"bindings", "verify", "api", "--all", "--poll-interval=1ms", "--wait-timeout=1s"}); code != 1 {
		t.Fatalf("exit = %d, want 1 when a binding fails; output = %s", code, out.String())
	}
	if !reflect.DeepEqual(created, []string{"billing", "email"}) {
		t.Fatalf("verified services = %v, want stable sorted list", created)
	}
	var got serviceBindingProbeBatchReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode batch report: %v; output=%s", err, out.String())
	}
	if got.App != "api" || got.Total != 2 || got.Checked != 2 || got.Passed != 1 || got.Failed != 1 || len(got.Bindings) != 2 {
		t.Fatalf("batch summary = %+v", got)
	}
	if got.Bindings[0].Status != "passed" || got.Bindings[0].Service != "billing" {
		t.Fatalf("first binding result = %+v, want passing billing result", got.Bindings[0])
	}
	if got.Bindings[1].Status != "failed" || got.Bindings[1].Service != "email" || got.Bindings[1].Report.TLS.Status != "failed" {
		t.Fatalf("second binding result = %+v, want failed email TLS result", got.Bindings[1])
	}
}

func TestCmdBindingsVerifyAllRejectsAppsWithoutServiceBindings(t *testing.T) {
	var createCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/bindings" {
			_ = json.NewEncoder(w).Encode(api.AppBindingInventory{App: "api", VerificationScope: "default"})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks" {
			createCalls++
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	if code := run([]string{"bindings", "verify", "api", "--all"}); code == 0 {
		t.Fatal("verification succeeded with no declared service bindings")
	}
	if createCalls != 0 {
		t.Fatalf("created %d canary task(s) with no bindings", createCalls)
	}
}

func TestRenderAppBindingInventory(t *testing.T) {
	var out bytes.Buffer
	previousOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })

	renderAppBindingInventory(appBindingInventory{
		App: "api",
		Bindings: []appBindingInventoryItem{
			{
				Type: bindingTypePostgres, Name: "primary", Binding: "DATABASE_URL", Scope: "production", Access: "read_write", State: "ready",
				CredentialGeneration: int64Pointer(2), RotationPending: boolPointer(true),
			},
			{
				Type: bindingTypeObjectStorage, Name: "assets", Binding: "GREGALE_S3_ASSETS", Scope: "production", Access: "read_write", State: "ready",
				RotationPending: boolPointer(false),
			},
			{Type: bindingTypeQueue, Name: "jobs", Binding: "worker", Scope: "app", Access: "push", State: "active"},
			{
				Type: bindingTypeService, Name: "billing", Binding: "GREGALE_SERVICE_BILLING_URL",
				HTTPURL: "http://billing.svc.gregale:10081", HTTPSEnv: "GREGALE_SERVICE_BILLING_HTTPS_URL",
				HTTPSURL: "https://billing.internal", Transport: "http", Scope: "app", Access: "invoke", State: "enforced",
			},
		},
		Warnings: []string{managedPostgresBindingsWarning},
	})

	for _, want := range []string{
		"TYPE", "NAME", "BINDING", "SCOPE", "ACCESS", "CONFIGURATION", "CONSUMER", "LIVENESS", "GENERATION", "ROTATING", "CREDENTIAL CONFIGURED",
		"postgres", "DATABASE_URL", "queue", "worker", "GREGALE_SERVICE_BILLING_URL", "2", "true", "false",
		"http", "http://billing.svc.gregale:10081", "GREGALE_SERVICE_BILLING_HTTPS_URL", "https://billing.internal",
		"Warning: " + managedPostgresBindingsWarning,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func int64Pointer(value int64) *int64 { return &value }

func boolPointer(value bool) *bool { return &value }

func TestCmdBindingsVerifyRejectsMigrationBeforeTaskAdmission(t *testing.T) {
	var creates int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/api":
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"api"}`))
		case "/v1/postgres/databases":
			_, _ = w.Write([]byte(`{"items":[{"id":"db-1"}]}`))
		case "/v1/postgres/databases/db-1/bindings":
			_, _ = w.Write([]byte(`{"items":[{"app_id":"app-1","environment_key":"SCHEMA_DSN","access":"migration"}]}`))
		default:
			creates++
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	if code := run([]string{"bindings", "verify", "api", "--postgres", "SCHEMA_DSN"}); code != 1 || creates != 0 {
		t.Fatalf("verify exit=%d, tasks=%d", code, creates)
	}
}
