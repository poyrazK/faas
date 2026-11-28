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

// TestCmdRedisUnbind pins `gregale redis unbind`: it revokes the app's grant
// to call the cache (ADR-20261124174500) and says the variable stays set.
func TestCmdRedisUnbind(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		_, _ = w.Write([]byte(`{"cache":"sessions","app":"web"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	main.WithCwd(t, t.TempDir())
	var out bytes.Buffer
	code := main.CaptureStdoutSwap(t, &out, func() int { return main.CmdRedis([]string{"unbind", "sessions", "--app", "web"}) })
	if code != 0 || method != http.MethodDelete || path != "/v1/redis/sessions/bindings/web" ||
		!strings.Contains(out.String(), "web can no longer call sessions") || !strings.Contains(out.String(), "gregale env unset") {
		t.Fatalf("exit = %d %s %s stdout = %q", code, method, path, out.String())
	}
}

// TestCmdRedisCLI pins `gregale redis cli`: the command runs as the Redis
// built-in in a task of the cache's bound app, its reply is printed as is,
// arguments that look like flags reach Redis, and an app whose runtime
// predates the built-in is told to redeploy.
func TestCmdRedisCLI(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		bound      string
		task       string
		wantCode   int
		wantApp    string
		wantCmd    []string
		wantStdout string
		wantStderr string
	}{
		{
			name: "only bound app, flag-like args pass through", args: []string{"sessions", "LRANGE", "q", "0", "-1"}, bound: `["web"]`,
			task:    `{"id":"t1","status":"succeeded","stdout_tail":"1) \"a\"\n"}`,
			wantApp: "web", wantCmd: []string{"__gregale_redis_v1__", "sessions.svc.gregale:6379", "LRANGE", "q", "0", "-1"},
			wantStdout: "1) \"a\"\n",
		},
		{
			name: "--app picks among bound apps", args: []string{"sessions", "--app", "worker", "--", "PING"}, bound: `["web","worker"]`,
			task:    `{"id":"t1","status":"succeeded","stdout_tail":"PONG\n"}`,
			wantApp: "worker", wantCmd: []string{"__gregale_redis_v1__", "sessions.svc.gregale:6379", "PING"}, wantStdout: "PONG\n",
		},
		{name: "several bound apps need --app", args: []string{"sessions", "PING"}, bound: `["web","worker"]`, wantCode: 1, wantStderr: "pass --app"},
		{name: "no bound app names bind", args: []string{"sessions", "PING"}, bound: `[]`, wantCode: 1, wantStderr: "gregale redis bind sessions --app APP"},
		{name: "streaming command refused locally", args: []string{"sessions", "SUBSCRIBE", "c"}, bound: `["web"]`, wantCode: 1, wantStderr: "SUBSCRIBE"},
		{
			name: "old runtime is told to redeploy", args: []string{"sessions", "PING"}, bound: `["web"]`,
			task:    `{"id":"t1","status":"failed","exit_code":127,"failure":{"code":"command_not_found","message":"not found"}}`,
			wantApp: "web", wantCmd: []string{"__gregale_redis_v1__", "sessions.svc.gregale:6379", "PING"},
			wantCode: 127, wantStderr: "redeploy it",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotApp string
			var gotCmd []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/redis/sessions":
					_, _ = w.Write([]byte(`{"name":"sessions","bound_apps":` + tt.bound + `}`))
				case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/apps/") && strings.HasSuffix(r.URL.Path, "/tasks"):
					gotApp = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/apps/"), "/tasks")
					var req struct {
						Command []string `json:"command"`
					}
					_ = json.NewDecoder(r.Body).Decode(&req)
					gotCmd = req.Command
					_, _ = w.Write([]byte(tt.task))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_test")
			main.WithCwd(t, t.TempDir())
			stderr, restore := main.CaptureStderr(t)
			var out bytes.Buffer
			code := main.CaptureStdoutSwap(t, &out, func() int { return main.CmdRedis(append([]string{"cli"}, tt.args...)) })
			restore()
			if code != tt.wantCode || gotApp != tt.wantApp || strings.Join(gotCmd, " ") != strings.Join(tt.wantCmd, " ") ||
				out.String() != tt.wantStdout || !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Fatalf("exit = %d app = %q cmd = %q stdout = %q stderr = %q", code, gotApp, gotCmd, out.String(), stderr.String())
			}
		})
	}
}
