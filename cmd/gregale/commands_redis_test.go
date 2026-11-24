package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestCmdRedisListJSON pins `gregale redis list --json`: the API response
// is printed as JSON, not the table.
func TestCmdRedisListJSON(t *testing.T) {
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	t.Cleanup(func() { (*main.JsonOutput) = false })
	f := main.AuthedFakeAPI(t, `{"caches":[{"name":"sessions","memory_mb":256,"eviction_policy":"allkeys-lru","host":"sessions.redis.internal","port":6379}],"limit":1}`, 200)
	var out bytes.Buffer
	if code := main.CaptureStdoutSwap(t, &out, func() int { return main.CmdRedis([]string{"list"}) }); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if (*f.SawMethod()) != "GET" || (*f.SawPath()) != "/v1/redis" {
		t.Fatalf("route = %s %s, want GET /v1/redis", *f.SawMethod(), *f.SawPath())
	}
	var got struct {
		Caches []struct {
			Name string `json:"name"`
		} `json:"caches"`
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
	}
	if len(got.Caches) != 1 || got.Caches[0].Name != "sessions" || got.Limit != 1 {
		t.Fatalf("output = %+v", got)
	}
}

// TestCmdRedisBindCreatesMissingApp pins binding before an app's first deploy:
// an app that reads REDIS_URL at startup failed its first deploy's health
// check because bind needed the app to exist (prod trial 2026-11-24). Bind now
// creates the app, and never does so for a cache that does not exist.
func TestCmdRedisBindCreatesMissingApp(t *testing.T) {
	notFound := `{"status":404,"code":"not_found","title":"Not found","detail":"no such app"}`
	tests := []struct {
		name       string
		cacheFound bool
		appFound   bool
		wantCode   int
		wantCreate bool
		wantBind   bool
		wantOut    string
	}{
		{name: "missing app is created", cacheFound: true, wantCode: 0, wantCreate: true, wantBind: true, wantOut: "Created app web and set REDIS_URL"},
		{name: "existing app is bound", cacheFound: true, appFound: true, wantCode: 0, wantBind: true, wantOut: "Set REDIS_URL on web"},
		// 4 is the CLI's not-found exit code.
		{name: "missing cache creates nothing", wantCode: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var created, bound bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/redis/sessions":
					if !tt.cacheFound {
						w.WriteHeader(http.StatusNotFound)
						_, _ = w.Write([]byte(notFound))
						return
					}
					_, _ = w.Write([]byte(`{"name":"sessions","memory_mb":64}`))
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/web":
					if !tt.appFound {
						w.WriteHeader(http.StatusNotFound)
						_, _ = w.Write([]byte(notFound))
						return
					}
					_, _ = w.Write([]byte(`{"id":"app-1","slug":"web"}`))
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps":
					created = true
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"id":"app-1","slug":"web"}`))
				case r.Method == http.MethodPost && r.URL.Path == "/v1/redis/sessions/bindings":
					bound = true
					_, _ = w.Write([]byte(`{"cache":"sessions","app":"web","env_key":"REDIS_URL"}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_test")
			main.WithCwd(t, t.TempDir())
			var out bytes.Buffer
			code := main.CaptureStdoutSwap(t, &out, func() int { return main.CmdRedis([]string{"bind", "sessions", "--app", "web"}) })
			if code != tt.wantCode || created != tt.wantCreate || bound != tt.wantBind || !strings.Contains(out.String(), tt.wantOut) {
				t.Fatalf("exit = %d created = %v bound = %v out = %q; want %d %v %v %q", code, created, bound, out.String(), tt.wantCode, tt.wantCreate, tt.wantBind, tt.wantOut)
			}
		})
	}
}
