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

// TestCmdRedisBindExplainsMissingApp pins binding before an app's first deploy:
// an app that reads REDIS_URL at startup failed its first deploy's health
// check because bind needed the app to exist (prod trial 2026-11-24). Bind
// never creates the app, which only a deploy can shape; it names the order
// that works, and never blames a missing cache on the app.
func TestCmdRedisBindExplainsMissingApp(t *testing.T) {
	notFound := `{"status":404,"code":"not_found","title":"Not found","detail":"not found"}`
	// 4 is the CLI's not-found exit code.
	tests := []struct {
		name       string
		cacheFound bool
		appFound   bool
		wantCode   int
		wantStdout string
		wantStderr string
		notStderr  string
	}{
		{name: "existing app is bound", cacheFound: true, appFound: true, wantCode: 0, wantStdout: "Set REDIS_URL on web"},
		{name: "missing app names the order", cacheFound: true, wantCode: 4, wantStderr: "Deploy it once with `gregale deploy --name web`, then bind the cache and deploy again."},
		{name: "missing cache is not blamed on the app", wantCode: 4, notStderr: "does not exist yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var created bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/redis/sessions" && tt.cacheFound:
					_, _ = w.Write([]byte(`{"name":"sessions","memory_mb":64}`))
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/web" && tt.appFound:
					_, _ = w.Write([]byte(`{"id":"app-1","slug":"web"}`))
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps":
					created = true
					w.WriteHeader(http.StatusTeapot)
				case r.Method == http.MethodPost && r.URL.Path == "/v1/redis/sessions/bindings" && tt.cacheFound && tt.appFound:
					_, _ = w.Write([]byte(`{"cache":"sessions","app":"web","env_key":"REDIS_URL"}`))
				default:
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(notFound))
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_test")
			main.WithCwd(t, t.TempDir())
			stderr, restore := main.CaptureStderr(t)
			var out bytes.Buffer
			code := main.CaptureStdoutSwap(t, &out, func() int { return main.CmdRedis([]string{"bind", "sessions", "--app", "web"}) })
			restore()
			errOut := stderr.String()
			if code != tt.wantCode || created || !strings.Contains(out.String(), tt.wantStdout) || !strings.Contains(errOut, tt.wantStderr) ||
				(tt.notStderr != "" && strings.Contains(errOut, tt.notStderr)) {
				t.Fatalf("exit = %d created = %v stdout = %q stderr = %q", code, created, out.String(), errOut)
			}
		})
	}
}
