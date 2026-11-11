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

// Deleting an app with a live managed PostgreSQL binding or an object storage
// bucket is refused, so data-api delete refuses an app with buckets before it
// revokes anything, then revokes every binding the app has, on any database,
// before it deletes the app.
func TestDataAPIDeleteRevokesBindingsBeforeDeletingTheApp(t *testing.T) {
	noRoute := func(w http.ResponseWriter) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "not_found", "Not found", "no such API route"))
	}
	for _, tc := range []struct {
		name string
		// appBindings answers the app-scoped binding list: 200, 503, or 0 for
		// an older control plane without the route.
		appBindings int
		buckets     []api.ObjectBucket
		wantCode    int
		wantRevoked []string
		wantDeleted bool
	}{
		{name: "app-scoped bindings on any database", appBindings: http.StatusOK, wantRevoked: []string{"DELETE /v1/postgres/bindings/b-org"}, wantDeleted: true},
		{name: "older control plane lists account databases", wantRevoked: []string{"DELETE /v1/postgres/bindings/b-live"}, wantDeleted: true},
		{name: "managed postgres unavailable", appBindings: http.StatusServiceUnavailable, wantDeleted: true},
		{name: "buckets refuse before anything is revoked", appBindings: http.StatusOK, buckets: []api.ObjectBucket{{ID: "bkt-1", Name: "files"}}, wantCode: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			var calls []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/notes-data":
					main.WriteJSONTest(w, api.AppResponse{ID: "app-1", Slug: "notes-data", Type: "app"})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/notes-data/buckets":
					main.WriteJSONTest(w, api.ObjectBucketList{Items: tc.buckets, Enabled: true})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/notes-data/postgres-bindings":
					switch tc.appBindings {
					case 0:
						noRoute(w)
					case http.StatusOK:
						main.WriteJSONTest(w, api.AppPostgresBindingList{Items: []api.AppPostgresBinding{
							{ID: "b-org", Database: "org-db", State: "ready"},
							{ID: "b-going", Database: "org-db", State: "deleting"},
						}})
					default:
						api.WriteProblem(w, api.NewProblem(tc.appBindings, "managed_postgres_unavailable", "Unavailable", "managed PostgreSQL is not configured"))
					}
				case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
					main.WriteJSONTest(w, api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: "db-1", State: "ready"}}})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases/db-1/bindings":
					main.WriteJSONTest(w, api.ManagedPostgresBindingList{Items: []api.ManagedPostgresBinding{
						{ID: "b-live", AppID: "app-1", State: "ready"},
						{ID: "b-going", AppID: "app-1", State: "deleting"},
						{ID: "b-gone", AppID: "app-1", State: "deleted"},
						{ID: "b-other", AppID: "app-2", State: "ready"},
					}})
				case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/postgres/bindings/"):
					main.WriteJSONTest(w, api.ManagedPostgresBinding{ID: strings.TrimPrefix(r.URL.Path, "/v1/postgres/bindings/"), AppID: "app-1", State: "deleting"})
				case r.Method == http.MethodDelete && r.URL.Path == "/v1/apps/notes-data":
					w.WriteHeader(http.StatusNoContent)
				default:
					http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusTeapot)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			if code := main.CmdDataAPI([]string{"delete", "notes-data", "--yes"}); code != tc.wantCode {
				t.Fatalf("delete = %d, want %d; calls %v", code, tc.wantCode, calls)
			}
			var revoked []string
			deleted := false
			for _, call := range calls {
				switch {
				case call == "DELETE /v1/apps/notes-data":
					deleted = true
				case strings.HasPrefix(call, "DELETE "):
					revoked = append(revoked, call)
				}
			}
			if !slices.Equal(revoked, tc.wantRevoked) || deleted != tc.wantDeleted {
				t.Fatalf("revoked %v deleted %v, want %v %v", revoked, deleted, tc.wantRevoked, tc.wantDeleted)
			}
			if tc.wantDeleted && calls[len(calls)-1] != "DELETE /v1/apps/notes-data" {
				t.Fatalf("the app was not deleted last: %v", calls)
			}
		})
	}
}
