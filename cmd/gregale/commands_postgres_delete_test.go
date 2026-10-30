package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// `postgres delete` schedules the delete, and --purge deletes the database
// and its restore history at once (ADR-20261029174950). Either way it asks for
// the database's name unless the caller passes --yes or --quiet.
func TestPostgresDeleteAsksForTheDatabaseName(t *testing.T) {
	const id = "5b0c6f0e-6a55-4d3b-9b8e-2f1a0c9d7e61"
	var mu sync.Mutex
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
			_, _ = w.Write([]byte(`{"items":[{"id":"` + id + `","name":"orders","state":"ready"}]}`))
		case r.URL.Path == "/v1/postgres/databases/"+id:
			switch {
			case r.Method == http.MethodDelete && r.URL.Query().Get("purge") == "true":
				_, _ = w.Write([]byte(`{"id":"` + id + `","name":"orders","state":"deleting"}`))
			case r.Method == http.MethodDelete:
				_, _ = w.Write([]byte(`{"id":"` + id + `","name":"orders","state":"deleting","restorable_until":"2026-11-06T12:00:00Z"}`))
			default:
				_, _ = w.Write([]byte(`{"id":"` + id + `","name":"orders","state":"ready"}`))
			}
		default:
			http.Error(w, `{"status":404,"code":"not_found"}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	oldStdin, oldNonInteractive := *main.OsStdin, *main.NonInteractive
	t.Cleanup(func() { *main.OsStdin, *main.NonInteractive = oldStdin, oldNonInteractive })

	deleted := func() string {
		mu.Lock()
		defer mu.Unlock()
		for _, request := range requests {
			if strings.HasPrefix(request, http.MethodDelete+" ") {
				return request
			}
		}
		return ""
	}
	const scheduled, purged = "DELETE /v1/postgres/databases/" + id, "DELETE /v1/postgres/databases/" + id + "?purge=true"
	for _, tc := range []struct {
		name           string
		args           []string
		typed          string
		nonInteractive bool
		json           bool
		wantCode       int
		wantDeleted    string
		wantStderr     string
		wantStdout     string
	}{
		{name: "typed name schedules", args: []string{"orders"}, typed: "orders\n", wantDeleted: scheduled, wantStderr: `Type "orders" to confirm`,
			wantStdout: "Undo before 2026-11-06T12:00:00Z with: gregale postgres undelete orders"},
		{name: "the prompt names undelete", args: []string{id}, typed: "orders\n", wantDeleted: scheduled, wantStderr: "gregale postgres undelete orders"},
		{name: "the message names purge and the plan limit", args: []string{"orders", "--yes"}, wantDeleted: scheduled, wantStdout: "counts toward your plan's database limit. To delete it now: gregale postgres delete orders --purge"},
		{name: "purge says it cannot be undone", args: []string{"orders", "--purge"}, typed: "orders\n", wantDeleted: purged, wantStderr: "cannot be recovered", wantStdout: "is deleting."},
		{name: "purge with yes", args: []string{"--purge", "--yes", "orders"}, wantDeleted: purged},
		{name: "wrong name keeps the database", args: []string{"orders"}, typed: "y\n", wantCode: 130},
		{name: "quiet before the name", args: []string{"--quiet", "orders"}, wantDeleted: scheduled},
		{name: "non-interactive needs yes", args: []string{"orders"}, nonInteractive: true, wantCode: 1, wantStderr: "--yes"},
		{name: "json needs yes", args: []string{"orders"}, json: true, wantCode: 1, wantStderr: "--yes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests = nil
			*main.OsStdin = strings.NewReader(tc.typed)
			*main.NonInteractive = tc.nonInteractive
			main.WithResetJSONOutput(t, tc.json)
			stdout, stderr, restore := main.SwapIO(t)
			defer restore()
			if code := main.CmdPostgresDelete(tc.args); code != tc.wantCode {
				t.Fatalf("exit %d, want %d; stderr %q", code, tc.wantCode, stderr())
			}
			if got := deleted(); got != tc.wantDeleted {
				t.Fatalf("delete request %q, want %q; requests %v", got, tc.wantDeleted, requests)
			}
			if !strings.Contains(stderr(), tc.wantStderr) {
				t.Fatalf("stderr %q lacks %q", stderr(), tc.wantStderr)
			}
			if !strings.Contains(stdout.String(), tc.wantStdout) {
				t.Fatalf("stdout %q lacks %q", stdout.String(), tc.wantStdout)
			}
		})
	}
}
