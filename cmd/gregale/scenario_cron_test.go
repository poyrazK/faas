// adr: 20261022194510
package main_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
)

const (
	cronTestRun        = "cccccccccccccccccccccccccccccccc"
	cronTestDeployment = "11111111-1111-1111-1111-111111111111"
	cronTestTask       = "22222222-2222-2222-2222-222222222222"
)

// containedCronAPI creates a cron, answers its fire-now with a task that
// pins the cron's argv (or, by mode, something else), and records deletion.
type containedCronAPI struct {
	mode    string
	created api.CreateCronRequest
	deleted bool
}

func (c *containedCronAPI) CreateCron(_ context.Context, _ string, req api.CreateCronRequest) (api.CronResponse, error) {
	c.created = req
	return api.CronResponse{ID: "33333333-3333-3333-3333-333333333333", AppID: req.AppID}, nil
}

func (c *containedCronAPI) FireCron(context.Context, string) (api.FireCronResponse, error) {
	return api.FireCronResponse{RequestID: "44444444-4444-4444-4444-444444444444", Status: "pending"}, nil
}

func (c *containedCronAPI) GetFireCronRequest(context.Context, string) (api.FireCronRequestResponse, error) {
	if c.mode == "fire-failed" {
		reason := "contained command admission is unavailable"
		return api.FireCronRequestResponse{Status: "failed", Error: &reason}, nil
	}
	task := cronTestTask
	return api.FireCronRequestResponse{Status: "succeeded", TaskID: &task}, nil
}

func (c *containedCronAPI) DeleteCron(context.Context, string) error {
	c.deleted = true
	return nil
}

func (c *containedCronAPI) CreateAppTask(context.Context, string, api.CreateAppTaskRequest) (api.AppTaskResponse, error) {
	return api.AppTaskResponse{}, errors.New("a cron runs through its fire-now, not a manual task")
}

func (c *containedCronAPI) GetAppTask(context.Context, string, string) (api.AppTaskResponse, error) {
	argv := rehearsalcommand.ReleaseArgv(c.created.Command, c.created.CommandShell)
	if c.mode == "other-command" {
		argv = []string{"bin/drop"}
	}
	spec := rehearsalcommand.Spec{Version: 1, RunID: cronTestRun, DeploymentID: cronTestDeployment, Command: argv}
	command, _ := rehearsalcommand.Encode(spec)
	exit := 0
	row := api.AppTaskResponse{ID: cronTestTask, AppID: "app-id", Kind: api.AppTaskKindCron, DeploymentID: cronTestDeployment, Command: command,
		MaxOutputBytes: rehearsalcommand.MaxReceiptBytes, Status: api.AppTaskStatusSucceeded, ExitCode: &exit}
	raw, _ := rehearsalcommand.Receipt{Version: 1, TaskID: cronTestTask, SpecSHA256: spec.SHA256(), Stdout: []byte("synced")}.Marshal()
	row.StdoutTail = string(raw)
	return row, nil
}

func (c *containedCronAPI) CancelAppTask(context.Context, string, string) (api.AppTaskResponse, error) {
	return api.AppTaskResponse{}, nil
}

func TestTriggerCronRunsTheCronAsAContainedCommand(t *testing.T) {
	for _, tc := range []struct {
		mode, wantErr string
		shell         bool
		command       []string
	}{
		{mode: "complete", command: []string{"bin/sync", "--once"}},
		{mode: "shell", command: []string{"bin/sync --once"}, shell: true},
		{mode: "fire-failed", command: []string{"bin/sync"}, wantErr: "contained cron fire-now failed: contained command admission is unavailable"},
		{mode: "other-command", command: []string{"bin/sync"}, wantErr: "contained task intent differs"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			client := &containedCronAPI{mode: tc.mode}
			stages, err := main.RunContainedCron(t.Context(), client, cronTestRun, cronTestDeployment, "app-id", tc.command, tc.shell)
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if !client.deleted || client.created.AppID != "app-id" || !slices.Equal(client.created.Command, tc.command) || client.created.CommandShell != tc.shell {
				t.Fatalf("cron = %+v deleted=%v", client.created, client.deleted)
			}
			if tc.wantErr == "" && !slices.Equal(stages, []string{"cron:succeeded"}) {
				t.Fatalf("stages = %v", stages)
			}
		})
	}
}

func TestTriggerCronValidation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		contained bool
		command   []string
		shell     bool
		timeout   int
		ok        bool
	}{
		{"contained", true, []string{"bin/sync"}, false, 300, true},
		{"needs command execution", false, []string{"bin/sync"}, false, 0, false},
		{"empty command", true, nil, false, 0, false},
		{"shell with several words", true, []string{"bin/sync", "--once"}, true, 0, false},
		{"timeout too long", true, []string{"bin/sync"}, false, 3601, false},
	} {
		if err := main.ValidateTriggerCron(tc.contained, tc.command, tc.shell, tc.timeout); (err == nil) != tc.ok {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}
