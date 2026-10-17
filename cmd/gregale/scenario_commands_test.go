package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/behaviorcompare"
	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
)

type containedCommandAPI struct {
	row       api.AppTaskResponse
	mode      string
	cancelled bool
	cancel    context.CancelFunc
	spec      rehearsalcommand.Spec
}

func (c *containedCommandAPI) CreateAppTask(_ context.Context, _ string, req api.CreateAppTaskRequest) (api.AppTaskResponse, error) {
	spec, err := rehearsalcommand.Decode(req.Command)
	if err != nil {
		return api.AppTaskResponse{}, err
	}
	c.spec = spec
	c.row = api.AppTaskResponse{ID: "22222222-2222-2222-2222-222222222222", AppID: "app-id", Kind: api.AppTaskKindManual, DeploymentID: spec.DeploymentID, Command: req.Command, TimeoutSeconds: req.TimeoutSeconds, MaxOutputBytes: req.MaxOutputBytes, Status: api.AppTaskStatusQueued}
	if c.mode == "admission-failed" {
		return api.AppTaskResponse{}, errors.New("runner unavailable")
	}
	if c.cancel != nil {
		c.cancel()
	}
	return c.row, nil
}
func (c *containedCommandAPI) GetAppTask(context.Context, string, string) (api.AppTaskResponse, error) {
	row := c.row
	row.Status = api.AppTaskStatusSucceeded
	exit := 0
	row.ExitCode = &exit
	r := rehearsalcommand.Receipt{Version: 1, TaskID: row.ID, SpecSHA256: c.spec.SHA256(), Stdout: []byte("import complete"), Behavior: []byte(`{"version":1,"observations":[]}`)}
	if c.mode == "empty-artifact" {
		r.Behavior = []byte{}
	}
	if c.mode == "foreign-receipt" {
		r.TaskID = "33333333-3333-3333-3333-333333333333"
	}
	raw, _ := r.Marshal()
	row.StdoutTail = string(raw)
	if c.mode == "missing-receipt" {
		row.StdoutTail = ""
	}
	if c.mode == "wrong-deployment" {
		row.DeploymentID = "33333333-3333-3333-3333-333333333333"
	}
	return row, nil
}
func (c *containedCommandAPI) CancelAppTask(ctx context.Context, _ string, _ string) (api.AppTaskResponse, error) {
	if ctx.Err() != nil {
		return c.row, ctx.Err()
	}
	c.cancelled = true
	return c.row, nil
}

func TestContainedCommandRunnerHasNoHostFallbackAndPinsArtifacts(t *testing.T) {
	for _, mode := range []string{"complete", "admission-failed", "missing-receipt", "foreign-receipt", "wrong-deployment", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HOST_ONLY_SECRET", "must-not-travel")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := &containedCommandAPI{mode: mode}
			if mode == "cancelled" {
				client.cancel = cancel
			}
			output := filepath.Join(t.TempDir(), "behavior.json")
			marker := filepath.Join(t.TempDir(), "host-executed")
			receipt := &testRunReceipt{RunID: strings.Repeat("a", 32), DeploymentID: "11111111-1111-1111-1111-111111111111", AppSlug: "isolated", Profile: "warm", WorkloadAppIDs: map[string]string{"primary": "app-id"}}
			runner := testContainedCommandRunner{client: client, scenario: testScenario{Project: "primary", CommandExecution: &testCommandExecutionSpec{Version: 1, Runtime: "contained", Env: map[string]string{"EXPLICIT": "fixture"}}}, receipt: receipt, files: map[string][]byte{"fixture.json": []byte("{}")}}
			err := runner.run(ctx, "trigger", []string{"HOST_ONLY_SECRET=must-not-travel", "GREGALE_TEST_BEHAVIOR_OUTPUT=" + output}, []string{"/bin/sh", "-c", "touch " + marker})
			if (err == nil) != (mode == "complete") {
				t.Fatalf("mode %s: %v", mode, err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("recipe command ran on CLI host")
			}
			if _, ok := client.spec.Env["HOST_ONLY_SECRET"]; ok {
				t.Fatal("host environment was forwarded")
			}
			if client.spec.Env["GREGALE_TEST_URL"] != "http://primary.svc.gregale:10081" {
				t.Fatal("contained command was sent a host or production URL")
			}
			if mode == "complete" {
				raw, err := os.ReadFile(output)
				if err != nil || len(raw) == 0 || len(receipt.Commands) != 1 || receipt.Commands[0].ReceiptSHA256 == "" {
					t.Fatalf("missing artifact or durable receipt: %v %+v", err, receipt.Commands)
				}
			}
			if mode == "cancelled" && !client.cancelled {
				t.Fatal("cancelled CLI did not cancel its task")
			}
		})
	}
}

func TestContainedCommandUnsupportedEngineFailsBeforeHostExecution(t *testing.T) {
	s := testScenario{CommandExecution: &testCommandExecutionSpec{Version: 1, Runtime: "contained"}}
	for _, selection := range [][2]string{{"local", "local"}, {"simulated", "simulated"}, {"real-vm", "restored"}} {
		if err := validateTestCommandExecution(s, selection[0], selection[1]); err == nil {
			t.Fatal("unsupported engine or profile accepted")
		}
	}
}

func TestContainedCommandInputsMustMatchPinnedBehaviorBytes(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"fixture.json", "observer.js"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := testScenario{Project: "primary", Command: []string{"node", "observer.js"}, CommandExecution: &testCommandExecutionSpec{Version: 1, Runtime: "contained"}, Behavior: &testBehaviorSpec{Version: 1, Fixtures: []string{"fixture.json"}, Observers: []string{"observer.js"}, Observations: []behaviorcompare.Spec{{Key: "response", Kind: "http"}}}}
	var receipt testRunReceipt
	session, err := prepareTestBehavior(dir, s, &receipt)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(session.dir) }()
	files, err := prepareTestCommandFiles(dir, s)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.verifyCommandFiles(files); err != nil {
		t.Fatal(err)
	}
	files["fixture.json"] = []byte("changed-after-hashing")
	if err := session.verifyCommandFiles(files); err == nil {
		t.Fatal("different bytes were sent under the pinned fixture identity")
	}
}

func TestContainedCommandEmptyArtifactReplacesPriorObservationAndCleanupPreservesIt(t *testing.T) {
	for _, stage := range []string{"assertion", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "behavior.json")
			prior := []byte(`{"version":1,"observations":[{"key":"value","value":1}]}`)
			if err := os.WriteFile(output, prior, 0600); err != nil {
				t.Fatal(err)
			}
			receipt := &testRunReceipt{RunID: strings.Repeat("a", 32), DeploymentID: "11111111-1111-1111-1111-111111111111", AppSlug: "isolated", Profile: "warm", WorkloadAppIDs: map[string]string{"primary": "app-id"}}
			runner := testContainedCommandRunner{client: &containedCommandAPI{mode: "empty-artifact"}, scenario: testScenario{Project: "primary", CommandExecution: &testCommandExecutionSpec{Version: 1, Runtime: "contained"}}, receipt: receipt}
			if err := runner.run(t.Context(), stage, []string{"GREGALE_TEST_BEHAVIOR_OUTPUT=" + output}, []string{"true"}); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if stage == "assertion" && len(raw) != 0 {
				t.Fatal("empty final output retained stale observations")
			}
			if stage == "cleanup" && string(raw) != string(prior) {
				t.Fatal("cleanup rewrote the collected observations")
			}
		})
	}
}
