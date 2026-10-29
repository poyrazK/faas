package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// `postgres delete` removes the database and its restore history at the
// provider with no grace period, so it asks for the database's name unless
// the caller passes --yes or --quiet.
func TestPostgresDeleteAsksForTheDatabaseName(t *testing.T) {
	const id = "5b0c6f0e-6a55-4d3b-9b8e-2f1a0c9d7e61"
	var mu sync.Mutex
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
			_, _ = w.Write([]byte(`{"items":[{"id":"` + id + `","name":"orders","state":"ready"}]}`))
		case r.URL.Path == "/v1/postgres/databases/"+id:
			state := "ready"
			if r.Method == http.MethodDelete {
				state = "deleting"
			}
			_, _ = w.Write([]byte(`{"id":"` + id + `","name":"orders","state":"` + state + `"}`))
		default:
			http.Error(w, `{"status":404,"code":"not_found"}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	oldStdin, oldNonInteractive := *main.OsStdin, *main.NonInteractive
	t.Cleanup(func() { *main.OsStdin, *main.NonInteractive = oldStdin, oldNonInteractive })

	deleted := func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, request := range requests {
			if strings.HasPrefix(request, http.MethodDelete+" ") {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		name           string
		args           []string
		typed          string
		nonInteractive bool
		json           bool
		wantCode       int
		wantDeleted    bool
		wantStderr     string
	}{
		{name: "typed name deletes", args: []string{"orders"}, typed: "orders\n", wantDeleted: true, wantStderr: `Type "orders" to confirm`},
		{name: "by ID asks for the name", args: []string{id}, typed: "orders\n", wantDeleted: true, wantStderr: "cannot be recovered"},
		{name: "wrong name keeps the database", args: []string{"orders"}, typed: "y\n", wantCode: 130},
		{name: "yes skips the prompt", args: []string{"orders", "--yes"}, wantDeleted: true},
		{name: "quiet before the name", args: []string{"--quiet", "orders"}, wantDeleted: true},
		{name: "non-interactive needs yes", args: []string{"orders"}, nonInteractive: true, wantCode: 1, wantStderr: "--yes"},
		{name: "json needs yes", args: []string{"orders"}, json: true, wantCode: 1, wantStderr: "--yes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests = nil
			*main.OsStdin = strings.NewReader(tc.typed)
			*main.NonInteractive = tc.nonInteractive
			main.WithResetJSONOutput(t, tc.json)
			_, stderr, restore := main.SwapIO(t)
			defer restore()
			if code := main.CmdPostgresDelete(tc.args); code != tc.wantCode {
				t.Fatalf("exit %d, want %d; stderr %q", code, tc.wantCode, stderr())
			}
			if deleted() != tc.wantDeleted {
				t.Fatalf("requests %v; want deleted=%v", requests, tc.wantDeleted)
			}
			if !strings.Contains(stderr(), tc.wantStderr) {
				t.Fatalf("stderr %q lacks %q", stderr(), tc.wantStderr)
			}
		})
	}
}
