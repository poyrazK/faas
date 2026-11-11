package main_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDevDiagnosticReporterTargetsDeveloperApp(t *testing.T) {
	main.ResetJSONOut(t)
	out, restore := main.CaptureStdout(t)
	defer restore()
	*main.JsonOutput = true
	report := main.NewDevDiagnosticReporterForTest("/source/api", "dev-api-workspace")
	report(main.DevDiagnosticFromDeployment(api.DeploymentResponse{ID: "dep-123", Error: "line one\nline two"}, "image_build", ""))
	d, _ := main.ClassifyDevRuntimeLog("connect ECONNREFUSED 127.0.0.1:5432")
	report(d)
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two NDJSON records, got %q", out.String())
	}
	for i, line := range lines {
		var got main.DevDiagnostic
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatal(err)
		}
		if got.Event != "developer_diagnostic" || got.App != "dev-api-workspace" || got.SourceDir != "/source/api" {
			t.Fatalf("missing developer context: %+v", got)
		}
		want := "gregale logs --app dev-api-workspace --deployment dep-123"
		if i == 1 {
			want = "gregale logs --app dev-api-workspace --source runtime"
		}
		if got.LogsCommand != want {
			t.Fatalf("recovery command = %q, want %q", got.LogsCommand, want)
		}
	}
}

func TestDevDiagnosticReporterSurvivesWorkspaceTagging(t *testing.T) {
	main.ResetJSONOut(t)
	*main.JsonOutput = true
	var out, errOut bytes.Buffer
	tagger := main.NewDevDiagnosticTaggerForTest("api", "apps/api", &out, &errOut)
	oldOut := *main.OsStdout
	*main.OsStdout = tagger
	defer func() { *main.OsStdout = oldOut }()
	report := main.NewDevDiagnosticReporterForTest("/workspace/apps/api", "dev-api-workspace")
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			report(main.DevDiagnosticFromDeployment(api.DeploymentResponse{ID: "dep-123", Error: "failed\nwith context"}, "readiness", ""))
		})
	}
	wg.Wait()
	if err := tagger.Close(); err != nil {
		t.Fatal(err)
	}
	if errOut.Len() != 0 {
		t.Fatalf("tagger rejected diagnostics: %s", &errOut)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 20 {
		t.Fatalf("received %d records, want 20", len(lines))
	}
	for _, line := range lines {
		var got map[string]any
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatal(err)
		}
		if got["dev_project"] != "api" || got["dev_path"] != "apps/api" || got["app"] != "dev-api-workspace" || got["deployment_id"] != "dep-123" {
			t.Fatalf("lost diagnostic context: %v", got)
		}
	}
}

func TestDevDiagnosticReporterHumanRecovery(t *testing.T) {
	main.ResetJSONOut(t)
	*main.JsonOutput = false
	oldErr := *main.OsStderr
	var output bytes.Buffer
	*main.OsStderr = &output
	defer func() { *main.OsStderr = oldErr }()
	main.NewDevDiagnosticReporterForTest("/source/api", "dev-api-workspace")(main.DevDiagnosticFromDeployment(api.DeploymentResponse{ID: "dep-123", ErrorCode: api.CodeStageReadinessFailed}, "readiness", ""))
	if !strings.Contains(output.String(), "gregale logs --app dev-api-workspace --deployment dep-123") {
		t.Fatalf("missing developer log command: %s", &output)
	}
}

func TestDevDiagnosticRecoveryCommandOverridesLinkedProductionApp(t *testing.T) {
	main.SetupCLIRegression(t)
	root := t.TempDir()
	t.Chdir(root)
	oldRoot := *main.TestOnlyProjectContextRoot
	*main.TestOnlyProjectContextRoot = root
	defer func() { *main.TestOnlyProjectContextRoot = oldRoot }()
	if _, err := main.SaveProjectContext(root, main.LocalProjectContext{Version: 1, Project: "project-api", App: "production-api"}); err != nil {
		t.Fatal(err)
	}
	out, restore := main.CaptureStdout(t)
	defer restore()
	*main.JsonOutput = true
	main.NewDevDiagnosticReporterForTest(root, "dev-api-workspace")(main.DevDiagnosticFromDeployment(api.DeploymentResponse{ID: "dep-123", Error: "failed"}, "readiness", ""))
	var diagnostic main.DevDiagnostic
	if err := json.Unmarshal(out.Bytes(), &diagnostic); err != nil {
		t.Fatal(err)
	}
	preview, restorePreview := main.CaptureStdout(t)
	defer restorePreview()
	args := append(strings.Fields(diagnostic.LogsCommand)[1:], "--dry-run", "--json")
	if code := main.Run(args); code != 0 {
		t.Fatalf("recovery command returned %d", code)
	}
	var query struct {
		App     string `json:"app"`
		Filters struct {
			Deployment string `json:"deployment"`
		} `json:"filters"`
	}
	if err := json.Unmarshal(preview.Bytes(), &query); err != nil {
		t.Fatal(err)
	}
	if query.App != "dev-api-workspace" || query.Filters.Deployment != "dep-123" {
		t.Fatalf("recovery selected app=%s deployment=%s", query.App, query.Filters.Deployment)
	}
}
