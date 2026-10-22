package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/deploydiff"
	"gopkg.in/yaml.v3"
)

func TestPreviewMetadataPreservesDiffContract(t *testing.T) {
	diff := deploydiff.Diff{Slug: "sample-app"}
	var before, after bytes.Buffer
	if err := deploydiff.RenderJSON(&before, diff); err != nil {
		t.Fatal(err)
	}
	summary := main.NewCLIPreview("remote comparison", "sample-app", "", "commit abcdefg", "local changes excluded", "build not verified")
	if err := main.RenderDiffPreviewJSON(&after, diff, summary); err != nil {
		t.Fatal(err)
	}
	var old, updated map[string]json.RawMessage
	if err := json.Unmarshal(before.Bytes(), &old); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(after.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	for key, expected := range old {
		var expectedValue, actualValue bytes.Buffer
		_ = json.Compact(&expectedValue, expected)
		_ = json.Compact(&actualValue, updated[key])
		if !bytes.Equal(expectedValue.Bytes(), actualValue.Bytes()) {
			t.Fatalf("changed existing diff field %s", key)
		}
	}
	var metadata main.CliPreviewSummary
	if err := json.Unmarshal(updated["preview"], &metadata); err != nil || metadata.App != "sample-app" || metadata.LocalChanges != summary.LocalChanges {
		t.Fatalf("missing preview metadata: %s %v", after.String(), err)
	}
}

func TestRecoveryNextActionResumesOrGathersEvidence(t *testing.T) {
	for _, profile := range []string{"default", "staging"} {
		t.Run(profile, func(t *testing.T) {
			old := (*main.ProfileOverride)
			(*main.ProfileOverride) = profile
			t.Cleanup(func() { (*main.ProfileOverride) = old })
			waiting := main.NewDeployRecovery("readiness", "sample-app", "accepted-id", "", time.Minute, false)
			if waiting.NextCommand != waiting.ResumeCommand || !strings.Contains(waiting.NextCommand, "--profile "+profile+" deployment wait accepted-id") || strings.Contains(waiting.NextCommand, " deploy ") {
				t.Fatalf("unsafe wait recovery: %+v", waiting)
			}
			failure := main.FailedDeploymentRecovery("sample-app", api.DeploymentResponse{ID: "failed-id", Status: "failed"})
			if failure.ResumeCommand != "" || !strings.Contains(failure.NextCommand, "logs sample-app --deployment failed-id --explain") {
				t.Fatalf("wrong failed-build recovery: %+v", failure)
			}
			unknown := main.NewDeployRecovery("submit", "sample-app", "", "retry-id", 0, false)
			if unknown.NextCommand != unknown.InspectCommand || unknown.ResumeCommand != "" {
				t.Fatalf("unknown acceptance recommended resubmitting: %+v", unknown)
			}
		})
	}
}

func TestGithubWorkflowCarriesResolvedChoices(t *testing.T) {
	main.ResetJSONOut(t)
	source := t.TempDir()
	t.Chdir(source)
	main.StartTestLink(t, source, "sample-project", "sample-app", "staging")
	t.Setenv("FAAS_API", "https://staging.example.test")
	t.Setenv("GITHUB_REPOSITORY", "")
	t.Setenv("GITHUB_SHA", "")
	out, restore := main.CaptureStdout(t)
	defer restore()
	if code := main.CmdDeployTarball([]string{"--github", "--repo", "example/backend", "--ref", "release/v1"}); code != 0 {
		t.Fatalf("workflow exit=%d output=%s", code, out)
	}
	var workflow struct {
		Jobs map[string]struct {
			Environment string `yaml:"environment"`
			Steps       []struct {
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(out.Bytes(), &workflow); err != nil {
		t.Fatal(err)
	}
	job := workflow.Jobs["deploy"]
	if len(job.Steps) != 1 {
		t.Fatalf("invalid workflow: %s", out)
	}
	for key, value := range map[string]string{"app": "sample-app", "repo": "example/backend", "ref": "release/v1", "environment": "staging", "api-base": "https://staging.example.test"} {
		if job.Steps[0].With[key] != value {
			t.Errorf("input %s=%q, want %q", key, job.Steps[0].With[key], value)
		}
	}
	if job.Environment != "staging" {
		t.Fatalf("GitHub environment did not follow selection: %s", job.Environment)
	}
}

func TestGithubWorkflowRefusesUnrepresentedSettings(t *testing.T) {
	main.ResetJSONOut(t)
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"--github", "--name", "sample-app", "--vcpu", "2"}, {"--github", "--name", "sample-app", "--ref", "${{ secrets.TOKEN }}"}} {
		out, restore := main.CaptureStdout(t)
		if code := main.CmdDeployTarball(args); code == 0 || len(out.String()) != 0 {
			t.Errorf("unsafe workflow accepted: args=%v output=%s code=%d", args, out, code)
		}
		restore()
	}
}

func TestGithubWorkflowPreservesStringTargets(t *testing.T) {
	workflow := main.RenderGithubSnippet(main.GithubSnippetEnv{Environment: "true"}, "123", "", false)
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(workflow), &parsed); err != nil {
		t.Fatal(err)
	}
	job := parsed["jobs"].(map[string]any)["deploy"].(map[string]any)
	if job["environment"] != "true" {
		t.Fatalf("environment became a non-string YAML value: %#v", job["environment"])
	}
	inputs := job["steps"].([]any)[0].(map[string]any)["with"].(map[string]any)
	if inputs["app"] != "123" || inputs["environment"] != "true" {
		t.Fatalf("workflow changed literal targets: %#v", inputs)
	}
}

func TestSavedScalePlanRetainsVersionOneFields(t *testing.T) {
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("preview made a remote mutation: %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/v1/apps/sample-app":
			_, _ = w.Write([]byte(`{"slug":"sample-app","ram_mb":128}`))
		case "/v1/account":
			_, _ = w.Write([]byte(`{"plan":"hobby"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := main.NewClient(server.URL, "test")
	path := filepath.Join(t.TempDir(), "scale-plan.json")
	_, restore := main.CaptureStdout(t)
	defer restore()
	ram := 256
	if code := main.CmdAppScalePlan(client, main.EnvironmentAppClient{Client: client}, "sample-app", "", api.UpdateAppRequest{RAMMB: &ram}, path, nil); code != 0 {
		t.Fatalf("save scale plan exit=%d", code)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		SchemaVersion int                        `json:"schema_version"`
		Preview       map[string]json.RawMessage `json:"preview"`
	}
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.SchemaVersion != 1 || saved.Preview["context"] != nil {
		t.Fatalf("saved plan is incompatible with version-one readers: %s", body)
	}
}
