package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartRemembersAppAndRetainsAcceptedRecovery(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "nested"}[nested], func(t *testing.T) {
			startTestEnvironment(t)
			source := startTestSource(t)
			scope := source
			if nested {
				scope = t.TempDir()
			}
			runner := startTestRunner(t, scope, "")
			runner.session.SourcePath = source
			runner.session.AppID, runner.session.DeploymentID = "app-1", "accepted-1"
			if err := runner.rememberApp(); err != nil {
				t.Fatal(err)
			}
			link, path, err := findProjectContext(source)
			if err != nil || link.Version != standaloneContextVersion || link.Project != "" || link.App != runner.session.AppSlug || path != projectContextPath(source) {
				t.Fatalf("saved default: %+v, %s, %v", link, path, err)
			}
			ignored, err := os.ReadFile(filepath.Join(source, ".gitignore"))
			if err != nil || !strings.Contains(string(ignored), ".gregale/") {
				t.Fatalf("context is not ignored: %s, %v", ignored, err)
			}
			recovery := startTestRunner(t, scope, "\n")
			resume, proceed, err := recovery.chooseRecovery()
			if err != nil || !resume || !proceed || recovery.session.DeploymentID != "accepted-1" {
				t.Fatalf("recovery lost: resume=%t proceed=%t session=%+v err=%v", resume, proceed, recovery.session, err)
			}
		})
	}
}

func TestStartRememberPreservesChangedContext(t *testing.T) {
	startTestEnvironment(t)
	source := startTestSource(t)
	runner := startTestRunner(t, source, "")
	runner.session.AppID, runner.session.DeploymentID = "app-1", "accepted-1"
	startTestLink(t, source, "other-project", "other-app", "production")
	before, _ := os.ReadFile(projectContextPath(source))
	if err := runner.rememberApp(); err == nil {
		t.Fatal("a changed context was silently replaced")
	}
	after, _ := os.ReadFile(projectContextPath(source))
	if string(before) != string(after) {
		t.Fatal("changed context overwritten")
	}
}

func TestSourceDefaultsUseSelectedDirectory(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	caller, source := t.TempDir(), startTestSource(t)
	startTestLink(t, caller, "caller-project", "caller-app", "production")
	startTestLink(t, source, "source-project", "source-app", "preview")
	t.Chdir(caller)
	dir, err := deployContextDirectory(source)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := resolveEnvironmentFromContext("", dir)
	if err != nil || environment != "preview" {
		t.Fatalf("environment=%q err=%v", environment, err)
	}
	// Preview uses the same app resolution as the actual deployment, and
	// does not accept a project environment in its local inference mode.
	out, restore := captureStdout(t)
	defer restore()
	if code := cmdDeployTarball([]string{"--plan", "--path", source, "--environment="}); code != 0 {
		t.Fatalf("plan exit=%d: %s", code, out)
	}
	var receipt struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || receipt.Slug != "source-app" {
		t.Fatalf("wrong deployment target: %s, %v", out, err)
	}
}

func TestStandaloneContextDrivesMetricsAndCanBeChanged(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	root := t.TempDir()
	t.Chdir(root)
	if _, err := saveProjectContext(root, localProjectContext{Version: standaloneContextVersion, App: "saved-app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := environmentProjectSlug(""); err == nil || !strings.Contains(err.Error(), "standalone app") {
		t.Fatalf("project environment command did not explain the missing project: %v", err)
	}
	if project, err := environmentProjectSlug("explicit-project"); err != nil || project != "explicit-project" {
		t.Fatalf("explicit project was not honored: %q %v", project, err)
	}
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Method != http.MethodGet {
			t.Errorf("unexpected mutation: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"slug":"other-app"}`))
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", testAPIKey('a'))
	_, restore := captureStdout(t)
	defer restore()
	if code := cmdMetrics(nil); code != 0 {
		t.Fatalf("metrics exit=%d", code)
	}
	if len(paths) != 1 || paths[0] != "/v1/apps/saved-app/metrics" {
		t.Fatalf("wrong metrics target: %v", paths)
	}
	if code := cmdContextSet([]string{"--app", "other-app"}); code != 0 {
		t.Fatalf("context set exit=%d", code)
	}
	link, _, err := findProjectContext(root)
	if err != nil || link.App != "other-app" {
		t.Fatalf("context not updated: %+v, %v", link, err)
	}
}
