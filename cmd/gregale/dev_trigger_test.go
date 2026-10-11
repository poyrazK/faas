package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const devTriggerTestManifest = `triggers:
  - kind: cron
    app: demo-api
    schedule: "0 * * * *"
    path: /jobs/hourly
  - kind: cron
    app: demo-api
    schedule: "30 * * * *"
    path: /jobs/hourly
  - kind: cron
    app: demo-api
    schedule: "0 2 * * *"
    path: /jobs/nightly
  - kind: cron
    app: other-api
    schedule: "0 3 * * *"
    path: /jobs/other
`

func TestSelectDevCronPath(t *testing.T) {
	withManifest := t.TempDir()
	if err := os.WriteFile(filepath.Join(withManifest, "gregale.yaml"), []byte(devTriggerTestManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	single := t.TempDir()
	if err := os.WriteFile(filepath.Join(single, "gregale.yaml"), []byte("triggers:\n  - kind: cron\n    app: demo-api\n    schedule: \"0 * * * *\"\n    path: /tick\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, dir, project string
		rest               []string
		want, wantErr      string
	}{
		{name: "no manifest", dir: t.TempDir(), project: "demo-api", wantErr: "declares no cron triggers"},
		{name: "only other app crons", dir: withManifest, project: "solo-api", wantErr: "declares no cron triggers"},
		{name: "single cron is implicit", dir: single, project: "demo-api", want: "/tick"},
		{name: "several crons need a route", dir: withManifest, project: "demo-api", wantErr: "declares 2 cron triggers"},
		{name: "explicit route", dir: withManifest, project: "demo-api", rest: []string{"/jobs/nightly"}, want: "/jobs/nightly"},
		{name: "unknown route lists declared", dir: withManifest, project: "demo-api", rest: []string{"/jobs/other"}, wantErr: "/jobs/hourly, /jobs/nightly"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := selectDevCronPath(devTarget{Project: test.project, SourceDir: test.dir}, test.rest)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("selectDevCronPath = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestRejectDevTriggerAppSelection(t *testing.T) {
	_, restoreErr := captureStderr(t)
	defer restoreErr()
	for _, test := range []struct {
		verb string
		rest []string
		ok   bool
	}{
		{"invoke", []string{"--async", "--payload", "{}"}, true},
		{"invoke", []string{"--path", "/x", "production-api"}, false},
		{"delayed-task", []string{"--delay", "5m"}, true},
		{"delayed-task", []string{"--app", "production-api", "--delay", "5m"}, false},
		{"delayed-task", []string{"--app=production-api"}, false},
		{"cron", []string{"/jobs/nightly"}, true},
		{"cron", []string{"/a", "/b"}, false},
	} {
		if got := rejectDevTriggerAppSelection(test.verb, test.rest) == 0; got != test.ok {
			t.Errorf("%s %v accepted=%t, want %t", test.verb, test.rest, got, test.ok)
		}
	}
}

// devTriggerAPI fakes the two calls a trigger makes: the read-only session
// lookup and the delegated production endpoint.
type devTriggerAPI struct {
	mu       sync.Mutex
	requests []string
	invokes  []api.InvokeRequest
}

func (f *devTriggerAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/dev/sessions/demo-api":
			if len(r.URL.Query().Get("workspace_id")) != 32 {
				t.Errorf("session lookup missing workspace_id: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(api.DevSessionResponse{
				App:       api.AppResponse{Slug: "dev-demo-api-0123456789ab", URL: "https://dev-demo-api-0123456789ab.gregale.dev"},
				ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/dev/sessions/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"type":"about:blank","title":"Not found","status":404,"code":"not_found","detail":"no such developer session"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/dev-demo-api-0123456789ab/invoke":
			var req api.InvokeRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.invokes = append(f.invokes, req)
			_, _ = w.Write([]byte(`{"id":"inv-1","status":"completed"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
}

func setupDevTriggerCLI(t *testing.T, fake *devTriggerAPI) string {
	t.Helper()
	srv := fake.server(t)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte(devTriggerTestManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", testAPIKey('d'))
	t.Setenv("FAAS_DEVELOPER_ID", strings.Repeat("e", 32))
	return dir
}

func TestCmdDevTriggerTargetsDeveloperApp(t *testing.T) {
	for _, test := range []struct {
		name       string
		args       []string
		wantMethod string
		wantPath   string
	}{
		{"invoke passes flags through", []string{"--name", "demo-api", "invoke", "--method", "PUT", "--path", "/orders"}, "PUT", "/orders"},
		{"cron fires the declared route as POST", []string{"--name", "demo-api", "cron", "/jobs/nightly"}, "POST", "/jobs/nightly"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &devTriggerAPI{}
			setupDevTriggerCLI(t, fake)
			_, restoreOut := captureStdout(t)
			defer restoreOut()
			stderr, restoreErr := captureStderr(t)
			defer restoreErr()

			if code := cmdDev(append([]string{"trigger"}, test.args...)); code != 0 {
				t.Fatalf("cmdDev trigger = %d; stderr=%q", code, stderr.String())
			}
			if len(fake.invokes) != 1 || fake.invokes[0].Method != test.wantMethod || fake.invokes[0].Path != test.wantPath {
				t.Fatalf("invokes = %+v, want one %s %s", fake.invokes, test.wantMethod, test.wantPath)
			}
		})
	}
}

func TestCmdDevTriggerRejectsBeforeAnyRequest(t *testing.T) {
	fake := &devTriggerAPI{}
	setupDevTriggerCLI(t, fake)
	_, restoreErr := captureStderr(t)
	defer restoreErr()
	for _, args := range [][]string{
		{"trigger"},
		{"trigger", "--name", "demo-api", "unknown"},
		{"trigger", "--name", "demo-api", "invoke", "production-api"},
		{"trigger", "--name", "demo-api", "cron"}, // two routes declared
	} {
		if code := cmdDev(args); code == 0 {
			t.Errorf("cmdDev(%v) = 0, want failure", args)
		}
	}
	if len(fake.requests) != 0 {
		t.Fatalf("rejected triggers reached the API: %v", fake.requests)
	}
}

func TestCmdDevInfo(t *testing.T) {
	fake := &devTriggerAPI{}
	setupDevTriggerCLI(t, fake)
	stdout, restoreOut := captureStdout(t)
	defer restoreOut()
	stderr, restoreErr := captureStderr(t)
	defer restoreErr()

	if code := cmdDev([]string{"info", "--name", "demo-api"}); code != 0 {
		t.Fatalf("dev info = %d; stderr=%q", code, stderr.String())
	}
	for _, want := range []string{"https://dev-demo-api-0123456789ab.gregale.dev", "app:     dev-demo-api-0123456789ab", "project: demo-api"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("dev info output missing %q: %q", want, stdout.String())
		}
	}
	if code := cmdDev([]string{"info", "--name", "missing-api"}); code == 0 {
		t.Fatal("dev info for a missing session succeeded")
	}
	if !strings.Contains(stderr.String(), "start one with `gregale dev`") {
		t.Fatalf("missing-session hint absent: %q", stderr.String())
	}
	for _, request := range fake.requests {
		if !strings.HasPrefix(request, "GET ") {
			t.Fatalf("dev info made a non-GET request: %v", fake.requests)
		}
	}
}
