package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Placement is the operator's choice: with no --region, `add postgres` and
// `postgres create` create the database in the platform's default region
// instead of refusing (production-us offers a single region). Replaces the
// Hunt 14 H14-41 check that named the then-required --region.
func TestPostgresCreateRegionDefaulting(t *testing.T) {
	addPostgres := func(extra ...string) func() int {
		return func() int {
			return main.CmdAddPostgres(append([]string{"--app", "api", "--env", "default"}, extra...))
		}
	}
	createPostgres := func(extra ...string) func() int {
		return func() int { return main.CmdPostgres(append([]string{"create", "orders"}, extra...)) }
	}
	for _, tc := range []struct {
		name string
		run  func() int
		// capsForbidden answers capabilities with 403, as for a
		// postgres:manage key without postgres:read.
		capsForbidden bool
		wantRegion    string
		wantAsked     bool
	}{
		{name: "add postgres uses the default", run: addPostgres(), wantRegion: "us-east-2", wantAsked: true},
		{name: "postgres create uses the default", run: createPostgres(), wantRegion: "us-east-2", wantAsked: true},
		{name: "add postgres keeps an explicit region", run: addPostgres("--region", "eu-central-1"), wantRegion: "eu-central-1"},
		{name: "postgres create keeps an explicit region", run: createPostgres("--region", "eu-central-1"), wantRegion: "eu-central-1"},
		{name: "add postgres leaves an unreadable default to the server", run: addPostgres(), capsForbidden: true, wantRegion: "", wantAsked: true},
		{name: "postgres create leaves an unreadable default to the server", run: createPostgres(), capsForbidden: true, wantRegion: "", wantAsked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var askedDefault, created bool
			var createdRegion string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api":
					_, _ = w.Write([]byte(`{"id":"app-1","slug":"api"}`))
				case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
					_, _ = w.Write([]byte(`{"items":[]}`))
				case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/capabilities":
					if r.URL.Query().Get("region") == "" {
						askedDefault = true
					}
					if tc.capsForbidden {
						http.Error(w, `{"code":"forbidden"}`, http.StatusForbidden)
						return
					}
					region := r.URL.Query().Get("region")
					if region == "" {
						region = "us-east-2"
					}
					_, _ = w.Write([]byte(`{"region":"` + region + `","credential_access":["read_write"],"availability":["single_zone"]}`))
				case r.Method == http.MethodPost && r.URL.Path == "/v1/postgres/databases":
					var body struct {
						Region string `json:"region"`
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					created, createdRegion = true, body.Region
					http.Error(w, "stop after create", http.StatusTeapot)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_test")
			main.WithCwd(t, t.TempDir())
			_, restore := main.CaptureStderr(t)
			tc.run()
			restore()
			if !created || createdRegion != tc.wantRegion || askedDefault != tc.wantAsked {
				t.Fatalf("created = %v in %q, asked for the default = %v; want created in %q, asked = %v",
					created, createdRegion, askedDefault, tc.wantRegion, tc.wantAsked)
			}
		})
	}
}
