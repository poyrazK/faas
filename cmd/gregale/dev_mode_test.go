package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDevModeSelectionPrecedence(t *testing.T) {
	node := map[string]string{"package.json": `{"scripts":{"dev":"node --watch server.js","start":"node server.js"}}`}
	for _, tc := range []struct {
		name, manifest, command, wantMode, wantSource, wantCommand, wantError string
		watch, function                                                       bool
		explicit                                                              map[string]bool
	}{
		{name: "automatic", wantMode: "watch", wantSource: "detected", wantCommand: "npm run dev"},
		{name: "flag disables detection", explicit: map[string]bool{"watch": true}, wantMode: "build", wantSource: "flag"},
		{name: "manifest disables detection", manifest: "dev:\n  watch: false\n", wantMode: "build", wantSource: "manifest"},
		{name: "manifest command", manifest: "dev:\n  watch:\n    command: node custom.js\n", wantMode: "watch", wantSource: "manifest", wantCommand: "node custom.js"},
		{name: "flag disables manifest command", manifest: "dev:\n  watch:\n    command: node custom.js\n", explicit: map[string]bool{"watch": true}, wantMode: "build", wantSource: "flag"},
		{name: "flag enables disabled manifest", manifest: "dev:\n  watch: false\n", explicit: map[string]bool{"watch": true}, watch: true, wantMode: "watch", wantSource: "flag", wantCommand: "npm run dev"},
		{name: "flag keeps manifest command", manifest: "dev:\n  watch:\n    command: node custom.js\n", explicit: map[string]bool{"watch": true}, watch: true, wantMode: "watch", wantSource: "flag", wantCommand: "node custom.js"},
		{name: "command overrides disabled manifest", manifest: "dev:\n  watch: false\n", explicit: map[string]bool{"watch-command": true}, command: " node explicit.js ", wantMode: "watch", wantSource: "flag", wantCommand: "node explicit.js"},
		{name: "conflicting flags", explicit: map[string]bool{"watch": true, "watch-command": true}, command: "node explicit.js", wantError: "cannot be combined"},
		{name: "empty explicit command", explicit: map[string]bool{"watch-command": true}, command: " ", wantError: "nonempty"},
		{name: "functions never auto watch", function: true, wantMode: "function", wantSource: "default"},
		{name: "explicit function watch rejected", function: true, explicit: map[string]bool{"watch": true}, watch: true, wantError: "functions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{}
			for k, v := range node {
				files[k] = v
			}
			if tc.manifest != "" {
				files["gregale.yaml"] = tc.manifest
			}
			mode, err := main.ResolveDevModeForTest(writeDevLocalProject(t, files), tc.function, tc.explicit, tc.watch, tc.command)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error=%v, want %q", err, tc.wantError)
				}
				return
			}
			if err != nil || mode.Mode != tc.wantMode || mode.Source != tc.wantSource || mode.Command != tc.wantCommand {
				t.Fatalf("mode=%+v err=%v; want %s/%s/%q", mode, err, tc.wantMode, tc.wantSource, tc.wantCommand)
			}
		})
	}
}

func TestDevModeAutomaticCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		files                 map[string]string
		mode, command, reason string
	}{
		{name: "FastAPI", files: map[string]string{"requirements.txt": "fastapi\nuvicorn\n", "main.py": "from fastapi import FastAPI\napp = FastAPI()\n"}, mode: "watch", command: "uvicorn main:app --reload"},
		{name: "Go", files: map[string]string{"go.mod": "module example.com/api\n"}, mode: "build", reason: "unsupported_runtime"},
		{name: "Node without dev command", files: map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`}, mode: "build", reason: "no_development_command"},
		{name: "Dockerfile", files: map[string]string{"package.json": `{"scripts":{"dev":"vite"}}`, "Dockerfile": "FROM node:22\n"}, mode: "build", reason: "dockerfile"},
		{name: "custom Dockerfile", files: map[string]string{"package.json": `{"scripts":{"dev":"vite"}}`, "gregale.yaml": "build:\n  dockerfile: docker/Devfile\n", "docker/Devfile": "FROM node:22\n"}, mode: "build", reason: "dockerfile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode, err := main.ResolveDevModeForTest(writeDevLocalProject(t, tc.files), false, nil, false, "")
			if err != nil || mode.Mode != tc.mode || mode.Command != tc.command || mode.Reason != tc.reason {
				t.Fatalf("mode=%+v err=%v", mode, err)
			}
		})
	}
}

func TestDevModeUpsertFallbackAndConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		status       int
		code         string
		echo         bool
		wantCalls    int
		wantMode     string
		wantError    bool
	}{
		{name: "accepted", source: "detected", echo: true, wantCalls: 1, wantMode: "watch"},
		{name: "automatic unsupported", source: "detected", status: 422, code: api.CodeDevWatchUnsupported, wantCalls: 2, wantMode: "build"},
		{name: "explicit flag unsupported", source: "flag", status: 422, code: api.CodeDevWatchUnsupported, wantCalls: 1, wantError: true},
		{name: "explicit manifest unsupported", source: "manifest", status: 422, code: api.CodeDevWatchUnsupported, wantCalls: 1, wantError: true},
		{name: "no replay on unknown outcome", source: "detected", status: 503, code: "capacity_unavailable", wantCalls: 1, wantError: true},
		{name: "no replay on unrelated rejection", source: "detected", status: 422, code: "validation_failed", wantCalls: 1, wantError: true},
		{name: "older server reports watch off", source: "detected", wantCalls: 1, wantMode: "build"},
		{name: "explicit request not confirmed", source: "flag", wantCalls: 1, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var requests []api.UpsertDevSessionRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "PUT" || r.URL.Path != "/v1/dev/sessions/demo" {
					t.Errorf("unexpected %s %s", r.Method, r.URL)
				}
				var req api.UpsertDevSessionRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				requests = append(requests, req)
				if calls == 1 && tc.status != 0 {
					api.WriteProblem(w, api.NewProblem(tc.status, tc.code, "Watch unavailable", "This environment cannot run watch mode"))
					return
				}
				result := api.DevSessionResponse{App: api.AppResponse{Slug: "dev-demo"}}
				if tc.echo {
					result.Watch = req.Watch
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(result); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			request := api.UpsertDevSessionRequest{WorkspaceID: strings.Repeat("a", 32), LeaseSeconds: 3600, Postgres: &api.DevPostgresRequest{Region: "test-region"}, Buckets: []api.DevBucketRequest{{Name: "assets"}}}
			_, mode, err := main.UpsertDevSessionWithModeForTest(api.NewClient(server.URL, "test"), "demo", request, main.DevModeSelection{Mode: "watch", Source: tc.source, Command: "npm run dev"})
			if calls != tc.wantCalls || (err != nil) != tc.wantError || (!tc.wantError && mode.Mode != tc.wantMode) {
				t.Fatalf("calls=%d mode=%+v err=%v", calls, mode, err)
			}
			if len(requests) == 2 {
				if requests[0].Watch == nil || requests[1].Watch != nil {
					t.Fatalf("fallback watches=%+v", requests)
				}
				requests[0].Watch = nil
				if !reflect.DeepEqual(requests[0], requests[1]) {
					t.Fatal("fallback changed the reviewed target or resources")
				}
				if mode.Source != "server" || mode.Reason != api.CodeDevWatchUnsupported {
					t.Fatalf("missing fallback reason: %+v", mode)
				}
			}
		})
	}
}

func TestDevAllWatchFalseSurvivesChildLaunch(t *testing.T) {
	root := writeDevLocalProject(t, map[string]string{"package.json": `{"scripts":{"dev":"vite"}}`, "gregale.yaml": "dev:\n  watch: true\n"})
	disabled, enabled := false, true
	for _, tc := range []struct {
		watch *bool
		stop  bool
		want  string
	}{
		{want: ""}, {watch: &disabled, want: "--watch=false"}, {watch: &enabled, want: "--watch"}, {watch: &disabled, stop: true, want: ""},
	} {
		args := main.DevAllWatchArgsForTest(root, tc.watch, tc.stop)
		if tc.want != "" && !slices.Contains(args, tc.want) {
			t.Fatalf("child lost explicit choice: %v", args)
		}
		if tc.want == "" && slices.ContainsFunc(args, func(s string) bool { return strings.HasPrefix(s, "--watch") }) {
			t.Fatalf("child default/stop acquired watch flag: %v", args)
		}
		if tc.want == "--watch=false" {
			mode, err := main.ResolveDevModeForTest(root, false, map[string]bool{"watch": true}, false, "")
			if err != nil || mode.Mode != "build" || mode.Source != "flag" {
				t.Fatalf("manifest defeated parent opt-out: %+v %v", mode, err)
			}
		}
	}
}

func TestDevModeJSONIsOneTaggedLine(t *testing.T) {
	main.ResetJSONOut(t)
	oldJSON, oldOut := *main.JsonOutput, *main.OsStdout
	defer func() { *main.JsonOutput = oldJSON; *main.OsStdout = oldOut }()
	*main.JsonOutput = true
	var out bytes.Buffer
	*main.OsStdout = &out
	if err := main.ReportDevModeForTest(main.DevModeSelection{Mode: "watch", Source: "detected", Command: "npm run dev"}); err != nil {
		t.Fatal(err)
	}
	if bytes.Count(out.Bytes(), []byte("\n")) != 1 {
		t.Fatalf("mode is not NDJSON: %q", out.String())
	}
	var event map[string]any
	if err := json.NewDecoder(&out).Decode(&event); err != nil {
		t.Fatal(err)
	}
	if event["type"] != "developer_mode" || event["mode"] != "watch" || event["source"] != "detected" || event["command"] != "npm run dev" {
		t.Fatalf("mode event=%v", event)
	}
}

// Exercise the command boundary, not just the resolver: the first mutation
// must carry the chosen mode. Stop at a deliberate rejection before any build.
func TestCmdDevDispatchesResolvedMode(t *testing.T) {
	for _, tc := range []struct {
		name, manifest string
		args           []string
		wantWatch      bool
	}{
		{name: "plain dev", wantWatch: true},
		{name: "explicit off", args: []string{"--watch=false"}},
		{name: "manifest off", manifest: "dev:\n  watch: false\n"},
		{name: "explicit on overrides manifest", manifest: "dev:\n  watch: false\n", args: []string{"--watch"}, wantWatch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			files := map[string]string{"package.json": `{"scripts":{"dev":"node --watch server.js","start":"node server.js"}}`, "server.js": "require('http').createServer((q,s)=>s.end('ok')).listen(process.env.PORT);"}
			if tc.manifest != "" {
				files["gregale.yaml"] = tc.manifest
			}
			root := writeDevLocalProject(t, files)
			calls := 0
			var request api.UpsertDevSessionRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "PUT" || r.URL.Path != "/v1/dev/sessions/mode-app" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				api.WriteProblem(w, api.NewProblem(503, "fixture_stop", "Fixture", "Stop before deploying"))
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
			t.Setenv("FAAS_DEVELOPER_ID", strings.Repeat("a", 32))
			t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
			args := append([]string{"--name", "mode-app", "--path", root, "--once", "--no-logs"}, tc.args...)
			if code := main.Run(append([]string{"dev"}, args...)); code != 3 {
				t.Fatalf("dispatch returned %d", code)
			}
			if calls != 1 || (request.Watch != nil) != tc.wantWatch {
				t.Fatalf("calls=%d request=%+v", calls, request)
			}
			if request.Watch != nil && request.Watch.Command != "npm run dev" {
				t.Fatalf("wrong command: %+v", request.Watch)
			}
		})
	}
}

func TestDevSetupExplainsAutomaticModeWithoutMutation(t *testing.T) {
	main.ResetJSONOut(t)
	root := writeDevLocalProject(t, map[string]string{"package.json": `{"scripts":{"dev":"node --watch server.js","start":"node server.js"}}`, "server.js": "require('http').createServer((q,s)=>s.end('ok')).listen(process.env.PORT);"})
	var out bytes.Buffer
	oldOut, oldJSON := *main.OsStdout, *main.JsonOutput
	defer func() { *main.OsStdout = oldOut; *main.JsonOutput = oldJSON }()
	*main.OsStdout = &out
	*main.JsonOutput = true
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("local setup made a remote request: %s %s", r.Method, r.URL)
		w.WriteHeader(500)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	if code := main.CmdDev([]string{"setup", "--path", root, "--name", "mode-app"}); code != 0 {
		t.Fatalf("setup returned %d: %s", code, out.String())
	}
	var receipt struct {
		Development main.DevModeSelection `json:"development"`
	}
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Development.Mode != "watch" || receipt.Development.Source != "detected" || receipt.Development.Command != "npm run dev" {
		t.Fatalf("setup mode=%+v", receipt.Development)
	}
}
