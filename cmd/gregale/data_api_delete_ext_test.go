package main_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Deleting an app with a live managed PostgreSQL binding is refused, so
// data-api delete revokes the app's bindings first, then deletes the app.
func TestDataAPIDeleteRevokesBindingsBeforeDeletingTheApp(t *testing.T) {
	for _, tc := range []struct {
		name        string
		databases   int
		wantRevoked []string
	}{
		{name: "bindings", databases: http.StatusOK, wantRevoked: []string{"DELETE /v1/postgres/bindings/b-live"}},
		{name: "managed postgres unavailable", databases: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			var calls []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/notes-data":
					main.WriteJSONTest(w, api.AppResponse{ID: "app-1", Slug: "notes-data", Type: "app"})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
					if tc.databases != http.StatusOK {
						api.WriteProblem(w, api.NewProblem(tc.databases, "not_found", "Not found", "no such API route"))
						return
					}
					main.WriteJSONTest(w, api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: "db-1", State: "ready"}}})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases/db-1/bindings":
					main.WriteJSONTest(w, api.ManagedPostgresBindingList{Items: []api.ManagedPostgresBinding{
						{ID: "b-live", AppID: "app-1", State: "ready"},
						{ID: "b-going", AppID: "app-1", State: "deleting"},
						{ID: "b-gone", AppID: "app-1", State: "deleted"},
						{ID: "b-other", AppID: "app-2", State: "ready"},
					}})
				case r.Method == http.MethodDelete && r.URL.Path == "/v1/postgres/bindings/b-live":
					main.WriteJSONTest(w, api.ManagedPostgresBinding{ID: "b-live", AppID: "app-1", State: "deleting"})
				case r.Method == http.MethodDelete && r.URL.Path == "/v1/apps/notes-data":
					w.WriteHeader(http.StatusNoContent)
				default:
					http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusTeapot)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			if code := main.CmdDataAPI([]string{"delete", "notes-data", "--yes"}); code != 0 {
				t.Fatalf("delete = %d; calls %v", code, calls)
			}
			var revoked []string
			for _, call := range calls {
				if call != "DELETE /v1/apps/notes-data" && strings.HasPrefix(call, "DELETE ") {
					revoked = append(revoked, call)
				}
			}
			if !slices.Equal(revoked, tc.wantRevoked) {
				t.Fatalf("revoked %v, want %v", revoked, tc.wantRevoked)
			}
			if last := calls[len(calls)-1]; last != "DELETE /v1/apps/notes-data" {
				t.Fatalf("the app was not deleted last: %v", calls)
			}
		})
	}
}
