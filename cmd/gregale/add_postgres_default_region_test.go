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
func TestPostgresCreateWithoutRegionUsesPlatformDefault(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func() int
	}{
		{"add postgres", func() int { return main.CmdAddPostgres([]string{"--app", "api", "--env", "default"}) }},
		{"postgres create", func() int { return main.CmdPostgres([]string{"create", "orders"}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var askedDefault bool
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
					_, _ = w.Write([]byte(`{"region":"us-east-2","credential_access":["read_write"],"availability":["single_zone"]}`))
				case r.Method == http.MethodPost && r.URL.Path == "/v1/postgres/databases":
					var body struct {
						Region string `json:"region"`
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					createdRegion = body.Region
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
			if !askedDefault || createdRegion != "us-east-2" {
				t.Fatalf("asked for the default = %v, created in %q; want the platform default us-east-2", askedDefault, createdRegion)
			}
		})
	}
}
