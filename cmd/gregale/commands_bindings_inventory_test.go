package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func runBindingInventoryTest(t *testing.T, inventory api.AppBindingInventory, scope string, args ...string) (int, api.AppBindingInventory, string) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/api/bindings" || r.URL.Query().Get("scope") != scope {
			t.Errorf("unexpected inventory request: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(inventory)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	previousOut, previousErr, previousJSON := osStdout, osStderr, jsonOutput
	var stdout, stderr bytes.Buffer
	osStdout, osStderr = &stdout, &stderr
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = previousOut, previousErr, previousJSON })
	code := run(args)
	var got api.AppBindingInventory
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("inventory is not one JSON document: %v\n%s\n%s", err, stdout.String(), stderr.String())
	}
	if calls != 1 {
		t.Fatalf("inventory requests = %d, want one", calls)
	}
	return code, got, stdout.String() + stderr.String()
}

func TestCmdBindingsUsesSharedInventoryAndScope(t *testing.T) {
	for _, args := range [][]string{
		{"bindings", "api", "--scope", "staging", "--json"},
		{"--json", "bindings", "--scope", "staging", "api", "--require-complete"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			want := api.AppBindingInventory{App: "api", Scope: "staging", Complete: true, GeneratedAt: time.Now().UTC(),
				Bindings: []api.AppBindingInventoryItem{
					{Type: api.BindingTypeQueue, Name: "orders", Scope: "app", State: "enabled", ConsumerState: "active", ConsumerLiveness: "stale", RuntimeStatus: "stale", VerificationStatus: "unknown"},
					{Type: api.BindingTypePostgres, Name: "primary", Binding: "DATABASE_URL", Scope: "staging", State: "ready", RuntimeStatus: "unknown", VerificationStatus: "unknown"},
				},
			}
			code, got, _ := runBindingInventoryTest(t, want, "staging", args...)
			if code != 0 || !reflect.DeepEqual(got, want) {
				t.Fatalf("exit=%d inventory=%+v, want %+v", code, got, want)
			}
		})
	}
}

func TestCmdBindingsPartialInventoryExitStatus(t *testing.T) {
	for _, tc := range []struct {
		name, severity, code string
		required             bool
		exit                 int
	}{
		{"optional PostgreSQL", "warning", "managed_postgres_unavailable", false, 0},
		{"required optional PostgreSQL", "warning", "managed_postgres_unavailable", true, 1},
		{"failed query", "error", "query_failed", false, 1},
		{"forbidden metadata", "error", "forbidden", false, 1},
		{"consumer failure", "error", "consumer_status_unavailable", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := api.AppBindingInventory{App: "api", Complete: false, Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeService, Name: "billing"}},
				Issues:   []api.BindingInventoryIssue{{Type: api.BindingTypePostgres, Code: tc.code, Severity: tc.severity, Message: "Binding metadata is unavailable."}},
				Warnings: []string{"Binding metadata is unavailable."},
			}
			args := []string{"--json", "bindings", "api"}
			if tc.required {
				args = append(args, "--require-complete")
			}
			code, got, _ := runBindingInventoryTest(t, want, "", args...)
			if code != tc.exit || !reflect.DeepEqual(got, want) {
				t.Fatalf("exit=%d inventory=%+v", code, got)
			}
		})
	}
}

func TestCmdBindingsJSONUsesAnEmptyArray(t *testing.T) {
	code, got, output := runBindingInventoryTest(t, api.AppBindingInventory{App: "api", Complete: true, Bindings: []api.AppBindingInventoryItem{}}, "", "--json", "bindings", "api")
	if code != 0 || got.Bindings == nil || !strings.Contains(output, `"bindings": []`) {
		t.Fatalf("exit=%d output=%s", code, output)
	}
}

func TestCmdBindingsRejectsInvalidScopeBeforeRequest(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.NotFound(w, r) }))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	if code := run([]string{"bindings", "api", "--scope", "__all__"}); code != 1 || calls != 0 {
		t.Fatalf("exit=%d calls=%d", code, calls)
	}
}

func TestRenderIncompleteBindingInventoryDoesNotClaimNoBindings(t *testing.T) {
	previousOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })
	renderAppBindingInventory(appBindingInventory{App: "api", Bindings: []appBindingInventoryItem{}})
	if strings.Contains(out.String(), "No bindings for") || !strings.Contains(out.String(), "incomplete") {
		t.Fatalf("unknown inventory rendered as empty: %s", out.String())
	}
}
