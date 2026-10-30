package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// A scheduled delete shows its restorable_until in list and get, and
// `postgres undelete` brings the database back (ADR-20261029174950).
func TestPostgresUndeleteAndTheScheduledDeleteView(t *testing.T) {
	const id = "5b0c6f0e-6a55-4d3b-9b8e-2f1a0c9d7e61"
	const scheduled = `{"id":"` + id + `","name":"orders","region":"eu","service_class":"development","state":"deleting","restorable_until":"2026-11-06T12:00:00Z"}`
	undeleteStatus := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
			_, _ = w.Write([]byte(`{"items":[` + scheduled + `]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases/"+id:
			_, _ = w.Write([]byte(scheduled))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/postgres/databases/"+id+"/undelete":
			if undeleteStatus != http.StatusOK {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(undeleteStatus)
				_, _ = w.Write([]byte(`{"status":404,"code":"not_found","detail":"managed postgres database not found"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"` + id + `","name":"orders","state":"ready"}`))
		default:
			http.Error(w, `{"status":404,"code":"not_found"}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")

	for _, tc := range []struct {
		name       string
		run        func([]string) int
		args       []string
		gone       bool
		wantCode   int
		wantStdout []string
		wantStderr string
	}{
		{name: "list notes the window", run: main.CmdPostgresList, wantStdout: []string{"orders is scheduled for deletion; undo before 2026-11-06T12:00:00Z with: gregale postgres undelete orders"}},
		{name: "get shows restorable_until", run: main.CmdPostgresGet, args: []string{"orders"}, wantStdout: []string{"restorable_until: 2026-11-06T12:00:00Z (gregale postgres undelete orders)"}},
		{name: "undelete by name", run: main.CmdPostgresUndelete, args: []string{"orders"}, wantStdout: []string{"state:            ready"}},
		{name: "undelete after the window", run: main.CmdPostgresUndelete, args: []string{id}, gone: true, wantCode: 4, wantStderr: "managed postgres database not found"},
		{name: "undelete needs a database", run: main.CmdPostgresUndelete, wantCode: 1, wantStderr: "usage: gregale postgres undelete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			undeleteStatus = http.StatusOK
			if tc.gone {
				undeleteStatus = http.StatusNotFound
			}
			main.WithResetJSONOutput(t, false)
			stdout, stderr, restore := main.SwapIO(t)
			defer restore()
			if code := tc.run(tc.args); code != tc.wantCode {
				t.Fatalf("exit %d, want %d; stderr %q", code, tc.wantCode, stderr())
			}
			for _, want := range tc.wantStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("stdout %q lacks %q", stdout.String(), want)
				}
			}
			if !strings.Contains(stderr(), tc.wantStderr) {
				t.Fatalf("stderr %q lacks %q", stderr(), tc.wantStderr)
			}
		})
	}
}
