package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDiscoverDevAllApps(t *testing.T) {
	root := "/work/repo"
	tests := []struct {
		name           string
		sources        []projectSource
		discoverErr    error
		rootDeployable bool
		want           []devAllApp
		wantErr        string
	}{
		{
			name: "members sorted by path, root container skipped",
			sources: []projectSource{
				{Name: "web", RootDir: "apps/web", Path: "/work/repo/apps/web"},
				{Name: "api", RootDir: "apps/api", Path: "/work/repo/apps/api"},
			},
			rootDeployable: true,
			want: []devAllApp{
				{Project: "api", RootDir: "apps/api", SourceDir: "/work/repo/apps/api"},
				{Project: "web", RootDir: "apps/web", SourceDir: "/work/repo/apps/web"},
			},
		},
		{
			name:    "workload name is sanitized and empty name falls back to directory",
			sources: []projectSource{{Name: "Billing_Service", RootDir: "services/billing", Path: "/b"}, {Name: "", RootDir: "services/inventory", Path: "/i"}},
			want: []devAllApp{
				{Project: "billing-service", RootDir: "services/billing", SourceDir: "/b"},
				{Project: "inventory", RootDir: "services/inventory", SourceDir: "/i"},
			},
		},
		{
			name:           "deployable root with no members is the single app",
			rootDeployable: true,
			want:           []devAllApp{{Project: "repo", RootDir: ".", SourceDir: root}},
		},
		{
			name:    "nothing deployable",
			wantErr: "no deployable apps found",
		},
		{
			name:    "duplicate developer project names",
			sources: []projectSource{{Name: "api", RootDir: "a/api", Path: "/1"}, {Name: "api", RootDir: "b/api", Path: "/2"}},
			wantErr: `both map to developer project "api"`,
		},
		{
			name:        "scanner failure",
			discoverErr: errors.New("boom"),
			wantErr:     "boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := discoverDevAllApps(root,
				func(string) ([]projectSource, error) { return tt.sources, tt.discoverErr },
				func(string) bool { return tt.rootDeployable })
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("apps = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestDiscoverProjectSourcesFindsWorkspaceMembers(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", `{"name":"repo","private":true,"workspaces":["apps/*"]}`)
	write("apps/api/package.json", `{"name":"api","scripts":{"start":"node server.js"}}`)
	write("apps/api/server.js", "require('http').createServer().listen(process.env.PORT)\n")
	write("apps/web/package.json", `{"name":"web","scripts":{"start":"node server.js"}}`)
	write("apps/web/server.js", "require('http').createServer().listen(process.env.PORT)\n")

	apps, err := discoverDevAllApps(root, discoverProjectSources, func(dir string) bool { return detectShape(dir) != shapeUnknown })
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, app := range apps {
		got = append(got, app.Project+"="+app.RootDir)
	}
	if want := []string{"api=apps/api", "web=apps/web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("apps = %v, want %v", got, want)
	}
}

type fakeDevAllQuotaClient struct {
	account  api.AccountResponse
	whoErr   error
	existing map[string]bool
	histErr  error
	probed   []string
}

func (c *fakeDevAllQuotaClient) Whoami(context.Context) (api.AccountResponse, error) {
	return c.account, c.whoErr
}

func (c *fakeDevAllQuotaClient) GetDevSyncHistory(_ context.Context, project, workspaceID string, limit int) (api.DevSyncHistoryResponse, error) {
	c.probed = append(c.probed, project+"/"+workspaceID)
	if c.histErr != nil {
		return api.DevSyncHistoryResponse{}, c.histErr
	}
	if c.existing[project] {
		return api.DevSyncHistoryResponse{}, nil
	}
	return api.DevSyncHistoryResponse{}, &api.APIError{Problem: api.Problem{Status: 404}}
}

func TestPreflightDevAllQuota(t *testing.T) {
	apps := []devAllApp{{Project: "api"}, {Project: "web"}, {Project: "worker"}}
	workspaces := []string{"w1", "w2", "w3"}
	account := func(used, limit int) api.AccountResponse {
		a := api.AccountResponse{Plan: "hobby", DeveloperAppCount: used}
		a.Limits.DeveloperApps = limit
		return a
	}
	tests := []struct {
		name    string
		client  *fakeDevAllQuotaClient
		wantErr string
	}{
		{name: "all new within budget", client: &fakeDevAllQuotaClient{account: account(0, 5)}},
		{name: "existing environments reuse their slots", client: &fakeDevAllQuotaClient{account: account(2, 3), existing: map[string]bool{"api": true, "web": true}}},
		{name: "too many new environments", client: &fakeDevAllQuotaClient{account: account(1, 2), existing: map[string]bool{"api": true}},
			wantErr: "2 new developer environments are needed (web, worker) but only 1 of 2 are available on the hobby plan"},
		{name: "over-used account has zero available", client: &fakeDevAllQuotaClient{account: account(4, 2), existing: map[string]bool{"api": true, "web": true}},
			wantErr: "1 new developer environments are needed (worker) but only 0 of 2"},
		{name: "account read fails", client: &fakeDevAllQuotaClient{whoErr: errors.New("offline")}, wantErr: "load developer environment budget: offline"},
		{name: "probe fails for a reason other than not found", client: &fakeDevAllQuotaClient{account: account(0, 5), histErr: &api.APIError{Problem: api.Problem{Status: 500}}},
			wantErr: "check developer environment api"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := preflightDevAllQuota(context.Background(), tt.client, apps, workspaces)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				if want := []string{"api/w1", "web/w2", "worker/w3"}; !reflect.DeepEqual(tt.client.probed, want) {
					t.Fatalf("probed = %v, want %v", tt.client.probed, want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestDevAllChildArgs(t *testing.T) {
	app := devAllApp{Project: "api", RootDir: "apps/api", SourceDir: "/repo/apps/api"}
	tests := []struct {
		name string
		opts devAllOptions
		json bool
		want []string
	}{
		{name: "watch", want: []string{"dev", "--path", "/repo/apps/api", "--name", "api", "--json=false"}},
		{name: "once json", opts: devAllOptions{once: true, noLogs: true}, json: true,
			want: []string{"dev", "--path", "/repo/apps/api", "--name", "api", "--once", "--no-logs", "--json"}},
		{name: "stop", opts: devAllOptions{stop: true}, want: []string{"dev", "--path", "/repo/apps/api", "--name", "api", "--stop", "--json=false"}},
		{name: "postgres region open", opts: devAllOptions{open: true, postgres: true, postgresRegion: "eu-central-1"},
			want: []string{"dev", "--path", "/repo/apps/api", "--name", "api", "--open", "--postgres", "--postgres-region", "eu-central-1", "--json=false"}},
		{name: "ttl", opts: devAllOptions{ttl: "72h"},
			want: []string{"dev", "--path", "/repo/apps/api", "--name", "api", "--ttl", "72h", "--json=false"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.opts.childArgs(app, tt.json); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("args = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDevPrefixWriter(t *testing.T) {
	tests := []struct {
		name   string
		writes []string
		want   string
	}{
		{name: "whole lines", writes: []string{"a\nb\n"}, want: "[api] a\n[api] b\n"},
		{name: "line split across writes", writes: []string{"bu", "ild |", " ok\n"}, want: "[api] build | ok\n"},
		{name: "trailing partial line flushed on close", writes: []string{"done\npartial"}, want: "[api] done\n[api] partial\n"},
		{name: "empty line kept", writes: []string{"\n"}, want: "[api] \n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			w := &devPrefixWriter{mu: &sync.Mutex{}, out: &out, prefix: "[api] "}
			for _, chunk := range tt.writes {
				if n, err := w.Write([]byte(chunk)); err != nil || n != len(chunk) {
					t.Fatalf("Write = %d, %v", n, err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if out.String() != tt.want {
				t.Fatalf("output = %q, want %q", out.String(), tt.want)
			}
		})
	}
}

func TestDevJSONTagger(t *testing.T) {
	app := devAllApp{Project: "api", RootDir: "apps/api"}
	tests := []struct {
		name      string
		input     string
		wantLines []map[string]any
		wantErr   string
	}{
		{
			name:  "ndjson receipts are tagged",
			input: `{"type":"developer_sync","edit_to_live_ms":5}` + "\n" + `{"event":"developer_diagnostic","code":"x"}` + "\n",
			wantLines: []map[string]any{
				{"type": "developer_sync", "edit_to_live_ms": float64(5), "dev_project": "api", "dev_path": "apps/api"},
				{"event": "developer_diagnostic", "code": "x", "dev_project": "api", "dev_path": "apps/api"},
			},
		},
		{
			name:      "indented object becomes one line",
			input:     "{\n  \"project\": \"api\",\n  \"status\": \"stopped\"\n}\n",
			wantLines: []map[string]any{{"project": "api", "status": "stopped", "dev_project": "api", "dev_path": "apps/api"}},
		},
		{
			name:      "non-object value is wrapped",
			input:     `[1,2]`,
			wantLines: []map[string]any{{"value": []any{float64(1), float64(2)}, "dev_project": "api", "dev_path": "apps/api"}},
		},
		{
			name:      "undecodable output is preserved on stderr",
			input:     `{"ok":true}` + "\nnot json\n",
			wantLines: []map[string]any{{"ok": true, "dev_project": "api", "dev_path": "apps/api"}},
			wantErr:   "[api] not json\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			w := newDevJSONTagger(app, &sync.Mutex{}, &out, &errOut)
			if _, err := io.WriteString(w, tt.input); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			if len(lines) != len(tt.wantLines) {
				t.Fatalf("lines = %q, want %d", lines, len(tt.wantLines))
			}
			for i, line := range lines {
				var got map[string]any
				if err := json.Unmarshal([]byte(line), &got); err != nil {
					t.Fatalf("line %d %q: %v", i, line, err)
				}
				if !reflect.DeepEqual(got, tt.wantLines[i]) {
					t.Fatalf("line %d = %v, want %v", i, got, tt.wantLines[i])
				}
			}
			if errOut.String() != tt.wantErr {
				t.Fatalf("stderr = %q, want %q", errOut.String(), tt.wantErr)
			}
		})
	}
}

// fakeDevAllProcess writes its scripted output, then exits with code either
// immediately or when it receives an interrupt.
type fakeDevAllProcess struct {
	code      int
	untilStop bool
	stopped   chan struct{}
	once      sync.Once
	signals   *[]string
	mu        *sync.Mutex
	project   string
}

func (p *fakeDevAllProcess) Wait() (int, error) {
	if p.untilStop {
		select {
		case <-p.stopped:
		case <-time.After(5 * time.Second):
			return 1, errors.New("fake process was never interrupted")
		}
	}
	return p.code, nil
}

func (p *fakeDevAllProcess) Signal(sig os.Signal) error {
	p.mu.Lock()
	*p.signals = append(*p.signals, p.project+":"+sig.String())
	p.mu.Unlock()
	p.once.Do(func() { close(p.stopped) })
	return nil
}

type fakeDevAllScript struct {
	stdout, stderr string
	code           int
	untilStop      bool
	launchErr      error
}

func fakeDevAllLauncher(scripts map[string]fakeDevAllScript, signals *[]string, launched *[][]string, mu *sync.Mutex) devAllLauncher {
	return func(args []string, stdout, stderr io.Writer) (devAllProcess, error) {
		project := args[4]
		mu.Lock()
		*launched = append(*launched, args)
		mu.Unlock()
		script := scripts[project]
		if script.launchErr != nil {
			return nil, script.launchErr
		}
		_, _ = io.WriteString(stdout, script.stdout)
		_, _ = io.WriteString(stderr, script.stderr)
		return &fakeDevAllProcess{code: script.code, untilStop: script.untilStop, stopped: make(chan struct{}), signals: signals, mu: mu, project: project}, nil
	}
}

func TestRunDevAll(t *testing.T) {
	apps := []devAllApp{
		{Project: "api", RootDir: "apps/api", SourceDir: "/r/apps/api"},
		{Project: "web", RootDir: "apps/web", SourceDir: "/r/apps/web"},
	}
	tests := []struct {
		name        string
		opts        devAllOptions
		json        bool
		scripts     map[string]fakeDevAllScript
		interrupt   bool
		wantCode    int
		wantStdout  []string
		wantStderr  []string
		notStderr   []string
		wantSignals int
	}{
		{
			name: "once succeeds for every app",
			opts: devAllOptions{once: true},
			scripts: map[string]fakeDevAllScript{
				"api": {stdout: "Developer sync live in 4s.\n"},
				"web": {stdout: "Developer sync live in 6s.\n"},
			},
			wantStdout: []string{"[api] Developer sync live in 4s.\n", "[web] Developer sync live in 6s.\n"},
			notStderr:  []string{"keep running"},
		},
		{
			name: "one failed app does not stop the other watcher",
			scripts: map[string]fakeDevAllScript{
				"api": {stderr: "build failed\n", code: 1},
				"web": {stdout: "watching for changes\n", untilStop: true},
			},
			interrupt:   true,
			wantCode:    1,
			wantStdout:  []string{"[web] watching for changes\n"},
			wantStderr:  []string{"[api] build failed\n", "[api] developer loop exited (code 1); other apps keep running"},
			wantSignals: 1,
		},
		{
			name: "launch failure is isolated",
			opts: devAllOptions{once: true},
			scripts: map[string]fakeDevAllScript{
				"api": {launchErr: errors.New("exec format error")},
				"web": {stdout: "live\n"},
			},
			wantCode:   1,
			wantStdout: []string{"[web] live\n"},
			wantStderr: []string{"[api] could not start developer loop: exec format error"},
		},
		{
			name: "json receipts carry the app",
			opts: devAllOptions{once: true},
			json: true,
			scripts: map[string]fakeDevAllScript{
				"api": {stdout: `{"type":"developer_sync"}` + "\n"},
				"web": {stdout: `{"type":"developer_sync"}` + "\n"},
			},
			wantStdout: []string{`"dev_project":"api"`, `"dev_project":"web"`, `"dev_path":"apps/web"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldJSON := jsonOutput
			jsonOutput = tt.json
			t.Cleanup(func() { jsonOutput = oldJSON })
			var (
				mu       sync.Mutex
				signals  []string
				launched [][]string
				stdout   bytes.Buffer
				stderr   bytes.Buffer
			)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.interrupt {
				go func() {
					time.Sleep(50 * time.Millisecond)
					cancel()
				}()
			}
			code := runDevAll(ctx, apps, tt.opts, fakeDevAllLauncher(tt.scripts, &signals, &launched, &mu), &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("code = %d, want %d (stderr %q)", code, tt.wantCode, stderr.String())
			}
			if len(launched) != len(apps) {
				t.Fatalf("launched %d children, want %d", len(launched), len(apps))
			}
			for _, want := range tt.wantStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("stdout %q missing %q", stdout.String(), want)
				}
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Fatalf("stderr %q missing %q", stderr.String(), want)
				}
			}
			for _, unwanted := range tt.notStderr {
				if strings.Contains(stderr.String(), unwanted) {
					t.Fatalf("stderr %q unexpectedly contains %q", stderr.String(), unwanted)
				}
			}
			if tt.interrupt && len(signals) < tt.wantSignals {
				t.Fatalf("signals = %v, want an interrupt forwarded to the running child", signals)
			}
		})
	}
}

func TestCmdDevAllFromFlagsRejectsSingleAppFlags(t *testing.T) {
	tests := []struct {
		name     string
		explicit map[string]bool
		opts     devAllOptions
		want     string
	}{
		{name: "name", explicit: map[string]bool{"name": true}, want: "--name cannot be combined with --all"},
		{name: "env file", explicit: map[string]bool{"env-file": true}, want: "--env-file cannot be combined with --all"},
		{name: "service override file", explicit: map[string]bool{"service-override-file": true}, want: "--service-override-file cannot be combined with --all"},
		{name: "region without postgres", opts: devAllOptions{postgresRegion: "eu"}, want: "--postgres-region requires --postgres"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stderr, restore := captureStderr(t)
			code := cmdDevAllFromFlags(t.TempDir(), "", tt.explicit, tt.opts)
			restore()
			if code != 1 {
				t.Fatalf("code = %d, want 1", code)
			}
			if got := stderr.String(); !strings.Contains(got, tt.want) {
				t.Fatalf("stderr = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCmdDevAllStopNeedsNoDiscoveryBudget(t *testing.T) {
	// --stop skips the quota preflight; with no deployable source the command
	// fails at discovery before any API call.
	stderr, restore := captureStderr(t)
	code := cmdDevAll(t.TempDir(), devAllOptions{stop: true})
	restore()
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if got := stderr.String(); !strings.Contains(got, "no deployable apps found") {
		t.Fatalf("stderr = %q", got)
	}
}
