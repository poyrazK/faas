package main_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestStartAutoSelectsUnambiguousSource(t *testing.T) {
	for _, kind := range []string{"app", "function", "workspace"} {
		t.Run(kind, func(t *testing.T) {
			stdout, stderr := main.StartTestEnvironment(t)
			root := main.ResolvedTempDir(t)
			source := root
			if kind == "workspace" {
				if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"private":true,"workspaces":["packages/*"]}`), 0o600); err != nil {
					t.Fatal(err)
				}
				source = filepath.Join(root, "packages", "api")
				if err := os.MkdirAll(source, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "function" {
				if err := os.WriteFile(filepath.Join(source, "handler.js"), []byte("exports.handler = async () => ({statusCode: 200, body: 'ok'});\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := templates.Materialize("hello-node", source); err != nil {
				t.Fatal(err)
			}
			runner := main.StartTestRunner(t, root, "still unread\n")
			if code := runner.SelectSource(); code != 0 || (*runner.Session()).SourcePath != source {
				t.Fatalf("code=%d source=%s stdout=%s stderr=%s", code, (*runner.Session()).SourcePath, stdout, stderr())
			}
			if line, err := (*(*runner.Prompt()).Reader()).ReadString('\n'); err != nil || line != "still unread\n" {
				t.Fatalf("source detection consumed input: %q %v", line, err)
			}
			if strings.Contains(stdout.String(), "Choose") {
				t.Fatalf("unnecessary source prompt: %s", stdout)
			}
		})
	}
}

func TestStartRejectsInvalidSourceBeforeAuthentication(t *testing.T) {
	for _, kind := range []string{"missing", "file", "multiple"} {
		t.Run(kind, func(t *testing.T) {
			_, stderr := main.StartTestEnvironment(t)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			path := filepath.Join(t.TempDir(), "source")
			if kind == "file" {
				if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"start", path}
			if kind == "multiple" {
				args = append(args, "extra")
			}
			if code := main.Run(args); code != 1 || requests.Load() != 0 || stderr() == "" {
				t.Fatalf("code=%d requests=%d stderr=%s", code, requests.Load(), stderr())
			}
		})
	}
}

func TestStartWorkspaceKeepsRunnableRootInPicker(t *testing.T) {
	stdout, stderr := main.StartTestEnvironment(t)
	root := main.ResolvedTempDir(t)
	if err := templates.Materialize("hello-node", root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"start":"node server.js"},"workspaces":["packages/*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "packages", "api")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := templates.Materialize("hello-node", child); err != nil {
		t.Fatal(err)
	}
	runner := main.StartTestRunner(t, root, "2\n")
	if code := runner.SelectSource(); code != 0 || (*runner.Session()).SourcePath != root || !strings.Contains(stdout.String(), "Deploy the root directory") {
		t.Fatalf("code=%d source=%s stdout=%s stderr=%s", code, (*runner.Session()).SourcePath, stdout, stderr())
	}
}

func TestStartEmptyDirectoryOffersStarter(t *testing.T) {
	stdout, stderr := main.StartTestEnvironment(t)
	root := main.ResolvedTempDir(t)
	runner := main.StartTestRunner(t, root, "\n\n\n")
	if code := runner.SelectSource(); code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr())
	}
	want := filepath.Join(root, "gregale-node")
	if (*runner.Session()).SourcePath != want || (*runner.Session()).Template != "hello-node" {
		t.Fatalf("starter selection=%+v", (*runner.Session()))
	}
	if _, err := os.Stat(filepath.Join(want, "package.json")); err != nil {
		t.Fatalf("starter was not created under the selected directory: %v", err)
	}
}

func TestStartPreviewShowsConnectionBuildAndResourcePolicy(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		exists     bool
		app        api.AppResponse
	}{
		{name: "new app", want: "Account plan defaults"},
		{name: "existing app", exists: true, app: api.AppResponse{RAMMB: 512, CPUMillicores: 500}, want: "512 MB RAM / 500 millicores (existing app)"},
		{name: "older API", exists: true, want: "Keep existing app settings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, _ := main.StartTestEnvironment(t)
			old := (*main.ProfileOverride)
			(*main.ProfileOverride) = "staging"
			t.Cleanup(func() { (*main.ProfileOverride) = old })
			source := main.StartTestSource(t)
			runner := main.StartTestRunner(t, source, "")
			runner.RenderLaunchDefaults(main.RunDoctorChecks(source), tc.app, tc.exists)
			for _, want := range []string{"Connection  staging", "Build       express (auto-detected)", "Resources   " + tc.want} {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("preview missing %q: %s", want, stdout)
				}
			}
		})
	}
}

func TestStartNextCommandsPreserveConnectionAndSource(t *testing.T) {
	for _, profile := range []string{"", "staging", "default"} {
		t.Run(profile, func(t *testing.T) {
			stdout, _ := main.StartTestEnvironment(t)
			old := (*main.ProfileOverride)
			(*main.ProfileOverride) = profile
			t.Cleanup(func() { (*main.ProfileOverride) = old })
			scope := filepath.Join(main.ResolvedTempDir(t), "my workspace")
			source := filepath.Join(scope, "app $demo")
			runner := main.StartTestRunner(t, source, "")
			(*runner.Session()).ScopePath = scope
			(*runner.Session()).DeploymentID = "deployment-1"
			runner.Finish()
			runner.PrintRecovery()
			cli := "gregale"
			if profile != "" {
				cli += " --profile " + profile
			}
			for _, want := range []string{
				cli + " deploy --path '" + source + "' --source=worktree --name first-app",
				cli + " logs first-app --deployment deployment-1 --follow", cli + " open first-app", cli + " apps rm first-app",
				cli + " start '" + scope + "'", cli + " deployment wait deployment-1",
			} {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("missing next command %q: %s", want, stdout)
				}
			}
			if strings.Contains(stdout.String(), "--yes") {
				t.Fatal("cleanup command bypasses confirmation")
			}
		})
	}
}
