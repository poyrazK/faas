package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 463 — customer health survives safe CLI decoding in both formats.
func TestCmdPostgresGetHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/postgres/databases/3f1c2a9e-8b7d-4c6e-9a1b-0d2e4f6a8c10" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"3f1c2a9e-8b7d-4c6e-9a1b-0d2e4f6a8c10","name":"orders","state":"ready","provider_resource_id":"private-upstream","connection_url":"private-password","health":{"enabled":true,"status":"degraded","fresh":true,"provider_status":"missing","compute_state":"unknown","checked_at":"2026-10-01T12:00:00Z","last_success_at":"2026-10-01T11:00:00Z","last_error_code":"resource_missing","stale_after_seconds":300}}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })
	for _, asJSON := range []bool{true, false} {
		var out bytes.Buffer
		(*main.OsStdout), (*main.JsonOutput) = &out, asJSON
		if code := main.CmdPostgresGet([]string{"3f1c2a9e-8b7d-4c6e-9a1b-0d2e4f6a8c10"}); code != 0 {
			t.Fatalf("exit = %d", code)
		}
		if strings.Contains(out.String(), "private-") {
			t.Fatal("CLI exposed private provider material")
		}
		if asJSON {
			var database api.ManagedPostgresDatabase
			if err := json.Unmarshal(out.Bytes(), &database); err != nil || database.Health == nil || database.Health.Status != "degraded" || database.Health.LastErrorCode != "resource_missing" {
				t.Fatalf("health JSON: %s %v", out.String(), err)
			}
		} else {
			for _, value := range []string{"provider_health:", "degraded", "compute_state:", "unknown", "health_checked:", "health_success:", "resource_missing"} {
				if !strings.Contains(out.String(), value) {
					t.Fatalf("human health output missing %q: %s", value, out.String())
				}
			}
		}
	}
}

func TestCmdPostgresListJSONUsesSafeEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/postgres/databases" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"db-1","name":"orders","region":"eu","service_class":"development","state":"ready","storage_limit_bytes":10737418240,"scale_to_zero":true,"connection_url":"should-never-be-rendered"}]}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	(*main.OsStdout), (*main.JsonOutput) = &out, true
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })

	if code := main.CmdPostgresList(nil); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var got api.ManagedPostgresDatabaseList
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if len(got.Items) != 1 || got.Items[0].Name != "orders" {
		t.Fatalf("unexpected output: %+v", got)
	}
	if strings.Contains(out.String(), "should-never-be-rendered") || strings.Contains(out.String(), "password") {
		t.Fatal("CLI output exposed connection material")
	}
}

func TestCmdPostgresCreateSendsProviderNeutralSpec(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/postgres/databases" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var req api.CreateManagedPostgresDatabaseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Name != "orders" || req.Region != "eu" || req.ServiceClass != "burstable" || req.Availability != "single_zone" || req.ScaleToZero == nil || *req.ScaleToZero || req.StorageLimitBytes != 123 {
			t.Fatalf("request = %+v", req)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"db-1","name":"orders","region":"eu","service_class":"burstable","availability":"single_zone","state":"provisioning","storage_limit_bytes":123,"scale_to_zero":false}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	(*main.OsStdout), (*main.JsonOutput) = &out, false
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })

	if code := main.CmdPostgresCreate([]string{"orders", "--region", "eu", "--class", "burstable", "--scale-to-zero=false", "--storage-bytes", "123"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "state:            provisioning") || strings.Contains(out.String(), "connection") {
		t.Fatalf("unexpected human output: %s", out.String())
	}
}

func TestCmdPostgresUsageJSONUsesSafeEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/account/managed-postgres-usage" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"period_start":"2026-09-01T00:00:00Z","observed_at":"2026-09-06T12:00:00Z","policy_enabled":true,"fresh":true,"guardrail_state":"healthy","ready_databases":1,"database_limit":3,"storage_limit_bytes":10737418240,"compute_unit_seconds":10,"storage_byte_seconds":20,"storage_byte_seconds_limit":30,"storage_byte_seconds_remaining":10,"history_byte_seconds":0,"egress_bytes":0,"cost_millicents":999,"provider_resource_id":"must-not-be-rendered"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	(*main.OsStdout), (*main.JsonOutput) = &out, true
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })

	if code := main.CmdPostgresUsage(nil); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var got api.ManagedPostgresUsageResponse
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got.GuardrailState != "healthy" || got.StorageByteSecondsRemaining != 10 {
		t.Fatalf("unexpected output: %+v", got)
	}
	if strings.Contains(out.String(), "cost_millicents") || strings.Contains(out.String(), "provider_resource_id") {
		t.Fatalf("CLI output exposed operator/provider fields: %s", out.String())
	}
}

// TestCmdPostgresUsageHumanOutputHasReadableUnits pins the canary output
// "storage_byte_seconds: 114070377600 / 2875910101401600000": raw meters stay
// for scripts, with GiB-months, CU-hours and IEC bytes alongside.
func TestCmdPostgresUsageHumanOutputHasReadableUnits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"period_start":"2026-10-01T00:00:00Z","policy_enabled":true,"fresh":true,"guardrail_state":"healthy","ready_databases":1,"database_limit":3,"compute_unit_seconds":5400,"storage_byte_seconds":114070377600,"storage_byte_seconds_limit":2875910101401600000,"storage_byte_seconds_remaining":2875909987331222400,"history_byte_seconds":0,"egress_bytes":1572864}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	(*main.OsStdout), (*main.JsonOutput) = &out, false
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })

	if code := main.CmdPostgresUsage(nil); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{
		"compute_unit_seconds:         5400 (1.50 CU-hours)",
		"storage_byte_seconds:         114070377600 / 2875910101401600000",
		"(0.03 GiB-hours of 1000.00 GiB-months used)",
		"history_byte_seconds:         0 (0.00 GiB-months)",
		"egress_bytes:                 1572864 (1.5 MiB)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

// adr: 20261025094915 — the usage output shows the allowance, ceiling and cap
// state, which old servers omit.
func TestCmdPostgresUsageShowsTheComputeCap(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want, not  []string
	}{
		{name: "capped", body: `{"period_start":"2026-10-01T00:00:00Z","policy_enabled":true,"fresh":true,"guardrail_state":"reached","ready_databases":1,"database_limit":1,"compute_unit_seconds":90000,"storage_byte_seconds":0,"storage_byte_seconds_limit":1,"storage_byte_seconds_remaining":1,"history_byte_seconds":0,"egress_bytes":0,"compute_allowance_unit_seconds":90000,"compute_ceiling_unit_seconds":90000,"compute_cap_state":"suspended"}`,
			want: []string{"compute_allowance:            25.00 CU-hours", "compute_ceiling:              25.00 CU-hours", "compute_cap_state:            suspended"}},
		{name: "old server", body: `{"period_start":"2026-10-01T00:00:00Z","policy_enabled":false,"fresh":false,"guardrail_state":"disabled","ready_databases":0,"database_limit":1,"compute_unit_seconds":0,"storage_byte_seconds":0,"storage_byte_seconds_limit":0,"storage_byte_seconds_remaining":0,"history_byte_seconds":0,"egress_bytes":0}`,
			not: []string{"compute_allowance", "compute_ceiling", "compute_cap_state"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_test")
			var out bytes.Buffer
			previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
			(*main.OsStdout), (*main.JsonOutput) = &out, false
			t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })
			if code := main.CmdPostgresUsage(nil); code != 0 {
				t.Fatalf("exit = %d", code)
			}
			for _, want := range tc.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output lacks %q:\n%s", want, out.String())
				}
			}
			for _, not := range tc.not {
				if strings.Contains(out.String(), not) {
					t.Errorf("output has %q:\n%s", not, out.String())
				}
			}
		})
	}
}

func TestRunDispatchesPostgresJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/postgres/bindings/binding-1" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"binding-1","database_id":"db-1","app_id":"app-1","scope":"production","environment_key":"DATABASE_URL","access":"read_write","credential_generation":1,"state":"ready"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	(*main.OsStdout), (*main.JsonOutput) = &out, false
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })

	if code := main.Run([]string{"--json", "postgres", "bindings", "get", "binding-1"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var got api.ManagedPostgresBinding
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got.EnvironmentKey != "DATABASE_URL" || got.State != "ready" {
		t.Fatalf("unexpected binding: %+v", got)
	}
}

func TestRunDispatchesPostgresBindingRotateJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/bindings/binding-1" {
			// The pre-rotate read that lets the CLI adopt a lost response.
			_, _ = w.Write([]byte(`{"id":"binding-1","database_id":"db-1","app_id":"app-1","scope":"production","environment_key":"DATABASE_URL","access":"read_write","credential_generation":1,"state":"ready"}`))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/postgres/bindings/binding-1/rotate" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"binding-1","database_id":"db-1","app_id":"app-1","scope":"production","environment_key":"DATABASE_URL","access":"read_write","credential_generation":2,"rotation_pending":true,"state":"ready"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	(*main.OsStdout), (*main.JsonOutput) = &out, false
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })

	if code := main.Run([]string{"--json", "postgres", "bindings", "rotate", "binding-1"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var got api.ManagedPostgresBinding
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got.CredentialGeneration != 2 || !got.RotationPending || got.State != "ready" {
		t.Fatalf("unexpected binding: %+v", got)
	}
}

func TestCmdPostgresBindingsRotateHumanOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/bindings/binding-1" {
			// The pre-rotate read that lets the CLI adopt a lost response.
			_, _ = w.Write([]byte(`{"id":"binding-1","database_id":"db-1","app_id":"app-1","scope":"production","environment_key":"DATABASE_URL","access":"read_write","credential_generation":1,"state":"ready"}`))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/postgres/bindings/binding-1/rotate" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"binding-1","database_id":"db-1","app_id":"app-1","scope":"production","environment_key":"DATABASE_URL","access":"read_write","credential_generation":2,"rotation_pending":true,"state":"ready"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	(*main.OsStdout), (*main.JsonOutput) = &out, false
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })

	if code := main.CmdPostgresBindingsRotate([]string{"binding-1"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if !strings.Contains(out.String(), "credential_generation: 2") || !strings.Contains(out.String(), "rotation_pending:  true") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestCmdPostgresBindingsRotateWaitReturnsCompletedBinding(t *testing.T) {
	var reads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/postgres/bindings/binding-1/rotate":
			_, _ = w.Write([]byte(`{"id":"binding-1","database_id":"db-1","app_id":"app-1","scope":"production","environment_key":"DATABASE_URL","access":"read_write","credential_generation":2,"rotation_pending":true,"state":"ready"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/bindings/binding-1":
			reads++
			pending := reads == 1
			_ = json.NewEncoder(w).Encode(api.ManagedPostgresBinding{
				ID: "binding-1", DatabaseID: "db-1", AppID: "app-1", Scope: "production",
				EnvironmentKey: "DATABASE_URL", Access: "read_write", CredentialGeneration: 2,
				RotationPending: pending, State: "ready",
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	(*main.OsStdout), (*main.JsonOutput) = &out, true
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })

	if code := main.CmdPostgresBindingsRotate([]string{"binding-1", "--wait", "--wait-timeout=1s", "--poll-interval=1ms"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	var got api.ManagedPostgresBinding
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got.ID != "binding-1" || got.RotationPending || reads != 2 {
		t.Fatalf("binding = %+v, status reads = %d", got, reads)
	}
}

func TestCmdPostgresBindingsRotateWaitTimeoutReturnsPendingJSON(t *testing.T) {
	var reads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/postgres/bindings/binding-1/rotate":
			_, _ = w.Write([]byte(`{"id":"binding-1","database_id":"db-1","app_id":"app-1","scope":"production","environment_key":"DATABASE_URL","access":"read_write","credential_generation":2,"rotation_pending":true,"state":"ready"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/bindings/binding-1":
			reads++
			_, _ = w.Write([]byte(`{"id":"binding-1","database_id":"db-1","app_id":"app-1","scope":"production","environment_key":"DATABASE_URL","access":"read_write","credential_generation":2,"rotation_pending":true,"state":"ready"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out, stderr bytes.Buffer
	previousOut, previousErr, previousJSON := (*main.OsStdout), (*main.OsStderr), (*main.JsonOutput)
	(*main.OsStdout), (*main.OsStderr), (*main.JsonOutput) = &out, &stderr, true
	t.Cleanup(func() {
		(*main.OsStdout), (*main.OsStderr), (*main.JsonOutput) = previousOut, previousErr, previousJSON
	})

	if code := main.CmdPostgresBindingsRotate([]string{"binding-1", "--wait", "--wait-timeout=10ms", "--poll-interval=1s"}); code != 1 {
		t.Fatalf("exit = %d, want timeout exit 1; output = %s", code, out.String())
	}
	var got api.ManagedPostgresBinding
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode latest binding: %v\n%s", err, out.String())
	}
	if !got.RotationPending || got.ID != "binding-1" {
		t.Fatalf("latest binding = %+v, want pending binding state", got)
	}
	if reads != 1 {
		t.Fatalf("status reads = %d, want only the pre-rotate read and no poll before timeout", reads)
	}
	if !strings.Contains(stderr.String(), "timed out after 10ms") {
		t.Fatalf("stderr = %q, want timeout detail", stderr.String())
	}
}

func TestCmdPostgresAttachResolvesSlugAndDatabaseName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api":
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"api"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
			_, _ = w.Write([]byte(`{"items":[{"id":"db-1","name":"orders","state":"ready"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/postgres/databases/db-1/bindings":
			var req api.CreateManagedPostgresBindingRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode binding request: %v", err)
			}
			if req.AppID != "app-1" || req.Scope != "production" || req.EnvironmentKey != "DATABASE_URL" || req.Access != "read_write" {
				t.Fatalf("binding request = %+v", req)
			}
			_, _ = w.Write([]byte(`{"id":"binding-1","database_id":"db-1","app_id":"app-1","scope":"production","environment_key":"DATABASE_URL","access":"read_write","state":"ready"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	(*main.OsStdout), (*main.JsonOutput) = &out, false
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })

	if code := main.CmdPostgresAttach([]string{"orders", "api", "--scope", "production"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if !strings.Contains(out.String(), "Attached orders to app api as DATABASE_URL") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestCmdPostgresAttachMigrationMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api":
			_, _ = w.Write([]byte(`{"id":"app-1","slug":"api"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
			_, _ = w.Write([]byte(`{"items":[{"id":"db-1","name":"orders","state":"ready"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/postgres/databases/db-1/bindings":
			var req api.CreateManagedPostgresBindingRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if req.Access != "migration" || req.EnvironmentKey != "MIGRATION_DATABASE_URL" {
				t.Errorf("migration request=%+v", req)
			}
			_, _ = w.Write([]byte(`{"id":"binding-1","database_id":"db-1","app_id":"app-1","scope":"production","environment_key":"MIGRATION_DATABASE_URL","access":"migration","state":"ready"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
	(*main.OsStdout), (*main.JsonOutput) = &out, false
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON })
	if code := main.CmdPostgresAttach([]string{"orders", "api", "--scope", "production", "--access", "migration", "--env", "MIGRATION_DATABASE_URL"}); code != 0 {
		t.Fatalf("migration attach exit=%d output=%s", code, out.String())
	}
}
