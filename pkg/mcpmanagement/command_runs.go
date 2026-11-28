package mcpmanagement

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretscan"
)

// Run a command in the app (ADR-20261117215858 §1). The command runs once in
// a fresh task VM from the live deployment (ADR-1037), never in a serving
// instance: no TTY, no stdin, bounded time and output. A plan reviews it and
// gregale_apply_operation admits it with the plan ID as idempotency key, so a
// retried apply never starts a second command. apid asks a person first
// before an agent connection runs a command on a production app.

const (
	runCommandDefaultTimeout = 120
	runCommandMaxTimeout     = 900
	runCommandDefaultOutput  = 64 * 1024
	runCommandMaxOutput      = 256 * 1024
	runCommandMaxArgs        = 64
	runCommandMaxArgBytes    = 4096
	runCommandMaxShellBytes  = 8192
	runCommandMaxWait        = 45
	runCommandPollInterval   = 2 * time.Second
)

type planRunCommandArgs struct {
	App            string   `json:"app" jsonschema:"Existing app slug; the command runs on its live default deployment"`
	Command        []string `json:"command,omitempty" jsonschema:"Program and arguments, e.g. [\"python\", \"manage.py\", \"migrate\"]; give command or shell"`
	Shell          string   `json:"shell,omitempty" jsonschema:"One shell command line run by /bin/sh -c; give command or shell"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty" jsonschema:"1 to 900; default 120"`
	MaxOutputBytes int      `json:"max_output_bytes,omitempty" jsonschema:"1024 to 262144 bytes of combined output kept; default 65536"`
	Reason         string   `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

type commandRunArgs struct {
	App         string `json:"app" jsonschema:"App slug"`
	TaskID      string `json:"task_id" jsonschema:"task id from the apply receipt"`
	WaitSeconds int    `json:"wait_seconds,omitempty" jsonschema:"Wait up to this many seconds (0 to 45) for the command to finish; default 0"`
}

type listCommandRunsArgs struct {
	App    string `json:"app" jsonschema:"App slug"`
	Limit  int    `json:"limit,omitempty" jsonschema:"1 to 50 runs, newest first; default 10"`
	Offset int    `json:"offset,omitempty" jsonschema:"Runs to skip, from next_offset of the previous page; default 0"`
}

type cancelCommandRunArgs struct {
	App    string `json:"app" jsonschema:"App slug"`
	TaskID string `json:"task_id" jsonschema:"task id from the apply receipt"`
}

func registerCommandRuns(s *mcp.Server, m *management) {
	register(s, m, "gregale_plan_run_command", "Review running one non-interactive command (argv or a shell line) in a fresh single-use task VM from the app's live deployment, with its variables and secrets: migrations, one-off scripts, checks from inside. Never touches serving instances. Bounded time and output. Changes nothing; apply with gregale_apply_operation, then read the result with gregale_get_command_run. On a production app an agent connection asks a person first.", m.planRunCommand)
	register(s, m, "gregale_list_command_runs", "List the app's recent command runs, newest first: one-off commands, release commands and command crons, with status and exit code but no output. Read one run's output with gregale_get_command_run.", m.listCommandRuns)
	register(s, m, "gregale_get_command_run", "Read one command run: status, exit code and the stdout/stderr tails, optionally waiting up to 45 s for it to finish. Lines that look like credentials are redacted.", m.getCommandRun)
	if m.opts.AllowWrites {
		registerTool(s, m, "gregale_cancel_command_run", "Ask a running command to stop. Work it already did is not undone. Idempotent.", false, m.cancelCommandRun)
	}
}

func (m *management) planRunCommand(ctx context.Context, a planRunCommandArgs) (any, error) {
	req := api.CreateAppTaskRequest{TimeoutSeconds: a.TimeoutSeconds, MaxOutputBytes: a.MaxOutputBytes}
	switch {
	case len(a.Command) > 0 && a.Shell != "", len(a.Command) == 0 && a.Shell == "":
		return nil, invalid("invalid_argument", "give exactly one of command (argv) or shell (one command line)")
	case a.Shell != "":
		if len(a.Shell) > runCommandMaxShellBytes || strings.ContainsRune(a.Shell, 0) {
			return nil, invalid("invalid_argument", "shell must be at most 8192 bytes without NUL")
		}
		req.Command, req.CommandShell = []string{a.Shell}, true
	default:
		if len(a.Command) > runCommandMaxArgs || a.Command[0] == "" {
			return nil, invalid("invalid_argument", "command needs a program and at most 63 arguments")
		}
		for _, arg := range a.Command {
			if len(arg) > runCommandMaxArgBytes || strings.ContainsRune(arg, 0) {
				return nil, invalid("invalid_argument", "each argument must be at most 4096 bytes without NUL")
			}
		}
		req.Command = append([]string(nil), a.Command...)
	}
	if req.TimeoutSeconds == 0 {
		req.TimeoutSeconds = runCommandDefaultTimeout
	}
	if req.MaxOutputBytes == 0 {
		req.MaxOutputBytes = runCommandDefaultOutput
	}
	if req.TimeoutSeconds < 1 || req.TimeoutSeconds > runCommandMaxTimeout {
		return nil, invalid("invalid_argument", "timeout_seconds must be from 1 to 900")
	}
	if req.MaxOutputBytes < 1024 || req.MaxOutputBytes > runCommandMaxOutput {
		return nil, invalid("invalid_argument", "max_output_bytes must be from 1024 to 262144")
	}
	if _, problem := req.Resolve(); problem != nil {
		return nil, invalid("invalid_argument", problem.Detail)
	}
	p, err := m.operationBase(ctx, operationArgs{App: a.App, Reason: a.Reason}, "run_command", req)
	if err != nil {
		return nil, err
	}
	p.Operation.Changes = p.Operation.Request
	p.TrafficEffect = "Runs the command once in a new task VM from the live deployment, with its variables and secrets. Serving instances and traffic are untouched, but the command can change data the app can reach. Read the result with gregale_get_command_run."
	return m.savePlan(p)
}

func (m *management) runCommand(ctx context.Context, p *releasePlan) (any, error) {
	var req api.CreateAppTaskRequest
	if err := json.Unmarshal(p.Operation.Request, &req); err != nil {
		return nil, err
	}
	req.Interactive, req.TTY = false, false
	task, err := m.client.CreateAppTask(ctx, p.App, req)
	if err != nil {
		return nil, err
	}
	if task.AppID != p.AppID {
		return nil, invalid("invalid_response", "task belongs to a different app")
	}
	return map[string]any{"task_id": task.ID, "deployment_id": task.DeploymentID, "status": task.Status, "next": "gregale_get_command_run with this task_id"}, nil
}

func (m *management) getCommandRun(ctx context.Context, a commandRunArgs) (any, error) {
	if err := validateSegment(a.TaskID, "task_id"); err != nil {
		return nil, err
	}
	if a.WaitSeconds < 0 || a.WaitSeconds > runCommandMaxWait {
		return nil, invalid("invalid_argument", "wait_seconds must be from 0 to 45")
	}
	app, _, err := m.appEnvironment(ctx, a.App, "", "", false)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(time.Duration(a.WaitSeconds) * time.Second)
	for {
		task, err := m.client.GetAppTask(ctx, a.App, a.TaskID)
		if err != nil {
			return nil, err
		}
		if task.AppID != app.ID {
			return nil, invalid("app_access_denied", "task belongs to a different app")
		}
		if task.Status.Terminal() || !time.Now().Add(runCommandPollInterval).Before(deadline) {
			return commandRunView(task), nil
		}
		select {
		case <-ctx.Done():
			return commandRunView(task), nil
		case <-time.After(runCommandPollInterval):
		}
	}
}

func (m *management) listCommandRuns(ctx context.Context, a listCommandRunsArgs) (any, error) {
	limit := a.Limit
	if limit == 0 {
		limit = 10
	}
	if limit < 1 || limit > 50 {
		return nil, invalid("invalid_argument", "limit must be from 1 to 50")
	}
	if a.Offset < 0 {
		return nil, invalid("invalid_argument", "offset must be zero or more")
	}
	app, _, err := m.appEnvironment(ctx, a.App, "", "", false)
	if err != nil {
		return nil, err
	}
	page, err := m.client.ListAppTasks(ctx, a.App, limit, a.Offset)
	if err != nil {
		return nil, err
	}
	runs := make([]map[string]any, 0, len(page.Tasks))
	for _, t := range page.Tasks {
		if t.AppID != app.ID {
			continue
		}
		run := map[string]any{"task_id": t.ID, "kind": t.Kind, "command": t.Command, "shell": t.CommandShell, "status": t.Status, "terminal": t.Status.Terminal(), "deployment_id": t.DeploymentID, "created_at": t.CreatedAt}
		if t.ExitCode != nil {
			run["exit_code"] = *t.ExitCode
		}
		if t.Failure != nil {
			run["failure"] = t.Failure
		}
		if t.FinishedAt != nil {
			run["finished_at"] = *t.FinishedAt
		}
		runs = append(runs, run)
	}
	out := map[string]any{"app": a.App, "runs": runs}
	if len(page.Tasks) == limit && page.NextOffset > 0 {
		out["next_offset"] = page.NextOffset
	}
	return out, nil
}

func (m *management) cancelCommandRun(ctx context.Context, a cancelCommandRunArgs) (any, error) {
	if err := validateSegment(a.TaskID, "task_id"); err != nil {
		return nil, err
	}
	app, _, err := m.appEnvironment(ctx, a.App, "", "", true)
	if err != nil {
		return nil, err
	}
	task, err := m.client.CancelAppTask(ctx, a.App, a.TaskID)
	if err != nil {
		return nil, err
	}
	if task.AppID != app.ID {
		return nil, invalid("app_access_denied", "task belongs to a different app")
	}
	return commandRunView(task), nil
}

func commandRunView(t api.AppTaskResponse) map[string]any {
	stdout, a := redactCommandOutput(t.StdoutTail)
	stderr, b := redactCommandOutput(t.StderrTail)
	out := map[string]any{"task_id": t.ID, "app_id": t.AppID, "deployment_id": t.DeploymentID, "command": t.Command, "shell": t.CommandShell, "status": t.Status, "terminal": t.Status.Terminal(),
		"stdout_tail": stdout, "stderr_tail": stderr, "output_truncated": t.OutputTruncated, "redacted_lines": a + b, "created_at": t.CreatedAt}
	if t.ExitCode != nil {
		out["exit_code"] = *t.ExitCode
	}
	if t.Failure != nil {
		out["failure"] = t.Failure
	}
	if t.StartedAt != nil {
		out["started_at"] = *t.StartedAt
	}
	if t.FinishedAt != nil {
		out["finished_at"] = *t.FinishedAt
	}
	if !t.Status.Terminal() {
		out["next"] = "call gregale_get_command_run again with wait_seconds"
	}
	return out
}

// redactCommandOutput replaces lines that look like credentials, such as an
// env dump, with a marker naming the kind of value. It is a guard against
// accidental disclosure, not a boundary: the command can print anything the
// deployment can read.
func redactCommandOutput(s string) (string, int) {
	if s == "" {
		return s, 0
	}
	findings := secretscan.ScanEnvContent("output", []byte(s))
	if len(findings) == 0 {
		return s, 0
	}
	lines := strings.Split(s, "\n")
	n := 0
	for _, f := range findings {
		if f.Line >= 1 && f.Line <= len(lines) && !strings.HasPrefix(lines[f.Line-1], "[redacted: ") {
			lines[f.Line-1] = "[redacted: " + f.Provider + "]"
			n++
		}
	}
	return strings.Join(lines, "\n"), n
}
