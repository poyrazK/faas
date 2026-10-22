package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
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
			main.StartTestEnvironment(t)
			source := main.StartTestSource(t)
			scope := source
			if nested {
				scope = t.TempDir()
			}
			runner := main.StartTestRunner(t, scope, "")
			(*runner.Session()).SourcePath = source
			(*runner.Session()).AppID, (*runner.Session()).DeploymentID = "app-1", "accepted-1"
			// The launch has already saved its accepted record before finishing.
			accepted := (*runner.StatePath())
			if err := main.SaveStartSession(accepted, (*runner.Session())); err != nil {
				t.Fatal(err)
			}
			if err := runner.RememberApp(); err != nil {
				t.Fatal(err)
			}
			records, err := filepath.Glob(filepath.Join(filepath.Dir(accepted), "*.json"))
			if err != nil || len(records) != 1 || records[0] != (*runner.StatePath()) || (*runner.StatePath()) == accepted {
				t.Fatalf("remembering the app must move, not copy, the recovery record: records=%v state=%s err=%v", records, (*runner.StatePath()), err)
			}
			link, path, err := main.FindProjectContext(source)
			if err != nil || link.Version != main.StandaloneContextVersion || link.Project != "" || link.App != (*runner.Session()).AppSlug || path != main.ProjectContextPath(source) {
				t.Fatalf("saved default: %+v, %s, %v", link, path, err)
			}
			ignored, err := os.ReadFile(filepath.Join(source, ".gitignore"))
			if err != nil || !strings.Contains(string(ignored), ".gregale/") {
				t.Fatalf("context is not ignored: %s, %v", ignored, err)
			}
			recovery := main.StartTestRunner(t, scope, "\n")
			resume, proceed, err := recovery.ChooseRecovery()
			if err != nil || !resume || !proceed || (*recovery.Session()).DeploymentID != "accepted-1" {
				t.Fatalf("recovery lost: resume=%t proceed=%t session=%+v err=%v", resume, proceed, (*recovery.Session()), err)
			}
		})
	}
}

func TestStartRememberMovesPendingRecoveryWithoutChangingOtherAccounts(t *testing.T) {
	main.StartTestEnvironment(t)
	source := main.StartTestSource(t)
	runner := main.StartTestRunner(t, source, "")
	(*runner.Session()).AppID, (*runner.Session()).DeploymentID, (*runner.Session()).VerifiedDeploymentID = "app-1", "previous-1", "previous-1"
	(*runner.Session()).PendingSubmission = &main.StartPendingSubmission{Key: "11111111-1111-4111-8111-111111111111", Transport: "resumable", UploadID: "pending-upload", TotalSize: 512}
	previousPath := (*runner.StatePath())
	if err := main.SaveStartSession(previousPath, (*runner.Session())); err != nil {
		t.Fatal(err)
	}
	other := (*runner.Session())
	other.AccountID, other.AppID, other.DeploymentID = "other-account", "other-app", "other-deployment"
	other.PendingSubmission = nil
	otherPath := main.StartContextSessionPath(previousPath, other.APIBase, other.AccountID)
	if err := main.SaveStartSession(otherPath, other); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.RememberApp(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(previousPath); !os.IsNotExist(err) {
		t.Fatalf("previous pending record was not retired: %v", err)
	}
	after, err := os.ReadFile(otherPath)
	if err != nil || string(before) != string(after) {
		t.Fatalf("another account's recovery record changed: %v", err)
	}
	recovery := main.StartTestRunner(t, source, "\n")
	resume, proceed, err := recovery.ChooseRecovery()
	saved := (*recovery.Session())
	if err != nil || !resume || !proceed || saved.DeploymentID != "previous-1" || saved.VerifiedDeploymentID != "previous-1" || saved.PendingSubmission == nil || *saved.PendingSubmission != *(*runner.Session()).PendingSubmission {
		t.Fatalf("pending recovery changed while remembering the app: resume=%t proceed=%t session=%+v err=%v", resume, proceed, saved, err)
	}
}

func TestStartRememberPreservesChangedContext(t *testing.T) {
	main.StartTestEnvironment(t)
	source := main.StartTestSource(t)
	runner := main.StartTestRunner(t, source, "")
	(*runner.Session()).AppID, (*runner.Session()).DeploymentID = "app-1", "accepted-1"
	main.StartTestLink(t, source, "other-project", "other-app", "production")
	before, _ := os.ReadFile(main.ProjectContextPath(source))
	if err := runner.RememberApp(); err == nil {
		t.Fatal("a changed context was silently replaced")
	}
	after, _ := os.ReadFile(main.ProjectContextPath(source))
	if string(before) != string(after) {
		t.Fatal("changed context overwritten")
	}
}

func TestSourceDefaultsUseSelectedDirectory(t *testing.T) {
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	caller, source := t.TempDir(), main.StartTestSource(t)
	main.StartTestLink(t, caller, "caller-project", "caller-app", "production")
	main.StartTestLink(t, source, "source-project", "source-app", "preview")
	t.Chdir(caller)
	dir, err := main.DeployContextDirectory(source)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := main.ResolveEnvironmentFromContext("", dir)
	if err != nil || environment != "preview" {
		t.Fatalf("environment=%q err=%v", environment, err)
	}
	// Preview uses the same app resolution as the actual deployment, and
	// does not accept a project environment in its local inference mode.
	out, restore := main.CaptureStdout(t)
	defer restore()
	if code := main.CmdDeployTarball([]string{"--plan", "--path", source, "--environment="}); code != 0 {
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
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	root := t.TempDir()
	t.Chdir(root)
	if _, err := main.SaveProjectContext(root, main.LocalProjectContext{Version: main.StandaloneContextVersion, App: "saved-app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := main.EnvironmentProjectSlug(""); err == nil || !strings.Contains(err.Error(), "standalone app") {
		t.Fatalf("project environment command did not explain the missing project: %v", err)
	}
	if project, err := main.EnvironmentProjectSlug("explicit-project"); err != nil || project != "explicit-project" {
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
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	_, restore := main.CaptureStdout(t)
	defer restore()
	if code := main.CmdMetrics(nil); code != 0 {
		t.Fatalf("metrics exit=%d", code)
	}
	if len(paths) != 1 || paths[0] != "/v1/apps/saved-app/metrics" {
		t.Fatalf("wrong metrics target: %v", paths)
	}
	if code := main.CmdContextSet([]string{"--app", "other-app"}); code != 0 {
		t.Fatalf("context set exit=%d", code)
	}
	link, _, err := main.FindProjectContext(root)
	if err != nil || link.App != "other-app" {
		t.Fatalf("context not updated: %+v, %v", link, err)
	}
}
