package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/onebox-faas/faas/pkg/api"
)

// The built-in Task release gate (ADR-20261123160000, amendment 1) releases Go
// and Python Tasks workers. The Node starter ships its own gate
// (tasks-release.js). The built-in gate runs entirely through the Gregale API,
// so the machine running the CLI needs no database credentials:
//
//   - The web app's release command applies the Task schema. The native adapter
//     deploys web before the candidate worker, so the schema is migrated before
//     any replacement starts.
//   - Readiness comes from the always-on observer's handler-compatibility
//     report: a report taken after the release started must show the candidate
//     deployment's live, non-draining workers for every handler version that
//     unfinished Tasks need.
//   - Previous workers are drained only after that, by the adapter's
//     conditional park, and the gate then waits until the report no longer lists
//     their deployments.

const (
	mcpWorkerRuntimeNode   = "node"
	mcpWorkerRuntimeGo     = "go"
	mcpWorkerRuntimePython = "python"
)

var mcpReleaseIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// mcpNativeWorkerRuntime names the Tasks starter a worker project came from.
func mcpNativeWorkerRuntime(workerPath string) (string, error) {
	regular := func(name string) bool {
		info, err := os.Lstat(filepath.Join(workerPath, name))
		return err == nil && info.Mode().IsRegular()
	}
	switch {
	case regular("tasks-release.js"):
		return mcpWorkerRuntimeNode, nil
	case regular("go.mod") && regular("task_schema.go") && regular("task_worker_registry.go"):
		return mcpWorkerRuntimeGo, nil
	case regular("task_schema.py") && regular("tasks.py"):
		return mcpWorkerRuntimePython, nil
	}
	return "", errors.New("native MCP Task releases need a worker project from a Tasks starter (mcp-node-tasks, mcp-go-tasks or mcp-python-tasks)")
}

// requireMCPNativeNodeWorker guards the plan review, recovery, quarantine and
// retirement flows: they need the Node gate's scripts, which the Go and Python
// starters do not have.
func requireMCPNativeNodeWorker(workerPath string) error {
	runtime, err := mcpNativeWorkerRuntime(workerPath)
	if err != nil {
		return err
	}
	if runtime != mcpWorkerRuntimeNode {
		return fmt.Errorf("this step needs a Node worker (mcp-node-tasks); a %s worker supports `gregale mcp server tasks deploy` and `deploy status` only, so recover or retire it with the manual rollout in docs/mcp.md", runtime)
	}
	return nil
}

// requireMCPWebTaskMigration checks that the web app's release command applies
// the Task schema, which is how a built-in release migrates: the native adapter
// deploys web, and so runs that command, before the candidate worker starts.
func requireMCPWebTaskMigration(webPath, runtime string) error {
	want := mcpStackMigrationCommand(runtime)
	body, err := os.ReadFile(filepath.Join(webPath, "gregale.yaml"))
	if err != nil || want == "" {
		return errors.New("web project needs a gregale.yaml whose release command applies the Task schema")
	}
	var cfg struct {
		Release struct {
			Command any `yaml:"command"`
		} `yaml:"release"`
	}
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		return fmt.Errorf("read web gregale.yaml: %w", err)
	}
	got := ""
	switch command := cfg.Release.Command.(type) {
	case string:
		got = strings.Join(strings.Fields(command), " ")
	case []any:
		parts := make([]string, 0, len(command))
		for _, part := range command {
			if text, ok := part.(string); ok {
				parts = append(parts, text)
			}
		}
		got = strings.Join(parts, " ")
	}
	if got != want {
		return fmt.Errorf("web release command must be %q so the Task schema is migrated before workers start", want)
	}
	return nil
}

// mcpGateView is the gate's view of the Task queue, read through the API.
type mcpGateView interface {
	// Baseline is the release start on the observer's clock: when its latest
	// report was taken, or the zero time when it has published none. Later
	// checks need a report taken after it, so a skewed local clock can
	// neither stall a release nor accept a report from before it.
	Baseline(ctx context.Context) (time.Time, error)
	// Ready reports whether a report taken after since shows the replacements'
	// deployment serving every handler version unfinished Tasks need.
	Ready(ctx context.Context, since time.Time, replacements []string) (bool, error)
	// PreviousGone reports whether such a report no longer lists any previous
	// worker deployment.
	PreviousGone(ctx context.Context, since time.Time) (bool, error)
}

type mcpGateCheckpoint struct {
	Version              int      `json:"version"`
	Namespace            string   `json:"namespace"`
	ReleaseID            string   `json:"releaseID"`
	PreviousWorkerIDs    []string `json:"previousWorkerIDs"`
	StartedAt            string   `json:"startedAt"`
	ReplacementWorkerIDs []string `json:"replacementWorkerIDs,omitempty"`
	Completed            bool     `json:"completed,omitempty"`
}

type mcpGateReport struct {
	OK                   bool     `json:"ok"`
	ExecutorSettled      bool     `json:"executorSettled"`
	Stage                string   `json:"stage"`
	ReplacementWorkerIDs []string `json:"replacementWorkerIDs,omitempty"`
}

type mcpGate struct {
	view           mcpGateView
	namespace      string
	releaseID      string
	timeout        time.Duration
	poll           time.Duration
	checkpointPath string
	migrate        func(ctx context.Context) error
	start          func(ctx context.Context, input map[string]any) ([]string, error)
	drain          func(ctx context.Context, input map[string]any) error
}

// run follows releaseMcpTasks in the Node starter. Errors never reach the
// report: hook and API errors can carry details that do not belong in it.
func (g *mcpGate) run(ctx context.Context) mcpGateReport {
	report := mcpGateReport{ExecutorSettled: true, Stage: "checkpoint"}
	checkpoint, err := g.loadCheckpoint()
	if err != nil || !mcpReleaseIDPattern.MatchString(g.releaseID) {
		return report
	}
	if checkpoint != nil && checkpoint.ReleaseID != g.releaseID {
		if !checkpoint.Completed {
			return report
		}
		checkpoint = nil
	}
	report.Stage = "migrations"
	if g.migrate(ctx) != nil {
		return report
	}
	report.Stage = "candidate_preflight"
	var since time.Time
	if checkpoint != nil {
		since, err = time.Parse(time.RFC3339Nano, checkpoint.StartedAt)
	} else {
		since, err = g.view.Baseline(ctx)
	}
	if err != nil {
		return report
	}
	// Worker IDs are not visible through the API; the adapter checks previous
	// generations by deployment, so the checkpoint keeps an empty ID list.
	record := mcpGateCheckpoint{Version: 1, Namespace: g.namespace, ReleaseID: g.releaseID, PreviousWorkerIDs: []string{}, StartedAt: since.UTC().Format(time.RFC3339Nano)}
	if checkpoint == nil && g.saveCheckpoint(record) != nil {
		return report
	}
	report.Stage = "start_replacements"
	ids := []string(nil)
	if checkpoint != nil {
		ids = checkpoint.ReplacementWorkerIDs
	}
	if ids == nil {
		if ids, err = g.start(ctx, map[string]any{"releaseID": g.releaseID, "previousWorkerIDs": record.PreviousWorkerIDs, "timeoutMs": g.timeout.Milliseconds()}); err != nil {
			return report
		}
	}
	if !validMCPWorkerIDs(ids, false) {
		return report
	}
	record.ReplacementWorkerIDs = ids
	if (checkpoint == nil || checkpoint.ReplacementWorkerIDs == nil) && g.saveCheckpoint(record) != nil {
		return report
	}
	report.Stage = "replacement_readiness"
	ready := func(ctx context.Context) (bool, error) { return g.view.Ready(ctx, since, ids) }
	finished := func(ctx context.Context) (bool, error) {
		ok, err := ready(ctx)
		if err != nil || !ok {
			return ok, err
		}
		return g.view.PreviousGone(ctx, since)
	}
	if checkpoint != nil && checkpoint.Completed {
		if g.wait(ctx, finished) != nil {
			return report
		}
		return mcpGateReport{OK: true, ExecutorSettled: true, Stage: "complete", ReplacementWorkerIDs: ids}
	}
	if g.wait(ctx, ready) != nil {
		return report
	}
	report.Stage = "drain_previous"
	if g.drain(ctx, map[string]any{"previousWorkerIDs": record.PreviousWorkerIDs, "replacementWorkerIDs": ids, "timeoutMs": g.timeout.Milliseconds()}) != nil {
		return report
	}
	if g.wait(ctx, finished) != nil {
		return report
	}
	report.Stage = "checkpoint_complete"
	record.Completed = true
	if g.saveCheckpoint(record) != nil {
		return report
	}
	return mcpGateReport{OK: true, ExecutorSettled: true, Stage: "complete", ReplacementWorkerIDs: ids}
}

func (g *mcpGate) wait(ctx context.Context, check func(context.Context) (bool, error)) error {
	deadline := time.Now().Add(g.timeout)
	for {
		ok, err := check(ctx)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if !time.Now().Before(deadline) {
			return errors.New("release deadline exceeded")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(g.poll, time.Until(deadline))):
		}
	}
}

func (g *mcpGate) loadCheckpoint() (*mcpGateCheckpoint, error) {
	if g.checkpointPath == "" {
		return nil, nil
	}
	body, err := os.ReadFile(g.checkpointPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var checkpoint mcpGateCheckpoint
	if json.Unmarshal(body, &checkpoint) != nil || checkpoint.Version != 1 || checkpoint.Namespace != g.namespace ||
		!mcpReleaseIDPattern.MatchString(checkpoint.ReleaseID) || !validMCPWorkerIDs(checkpoint.PreviousWorkerIDs, true) ||
		(checkpoint.Completed && checkpoint.ReplacementWorkerIDs == nil) ||
		(checkpoint.ReplacementWorkerIDs != nil && (!validMCPWorkerIDs(checkpoint.ReplacementWorkerIDs, false) ||
			slices.ContainsFunc(checkpoint.ReplacementWorkerIDs, func(id string) bool { return slices.Contains(checkpoint.PreviousWorkerIDs, id) }))) {
		return nil, errors.New("invalid or legacy release checkpoint")
	}
	if _, err := time.Parse(time.RFC3339Nano, checkpoint.StartedAt); err != nil {
		return nil, errors.New("invalid release checkpoint start")
	}
	return &checkpoint, nil
}

func (g *mcpGate) saveCheckpoint(record mcpGateCheckpoint) error {
	if g.checkpointPath == "" {
		return nil
	}
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	temporary := fmt.Sprintf("%s.%d.tmp", g.checkpointPath, os.Getpid())
	f, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return os.Rename(temporary, g.checkpointPath)
}

func validMCPWorkerIDs(ids []string, allowEmpty bool) bool {
	if ids == nil || (!allowEmpty && len(ids) == 0) || len(ids) > 4096 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !mcpWorkerUUID.MatchString(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

// mcpGateDeployments names the release's worker deployments: the candidate and
// the previous generations recorded in the release journal.
type mcpGateDeployments struct {
	Candidate string
	Previous  []string
}

var errMCPGateOutsideWorkers = errors.New("the Task namespace has workers outside this release's worker apps")

// mcpCompatibilityFresh reports whether report is a published, untruncated
// snapshot the observer took after since.
func mcpCompatibilityFresh(report api.MCPTaskCompatibilityReportResponse, since time.Time) (bool, error) {
	if !report.Published || report.Stale || report.ObservedAt == nil || !report.ObservedAt.After(since) {
		return false, nil
	}
	if report.Truncated {
		// A truncated report cannot prove every pending handler version.
		return false, errors.New("the observer's compatibility report is truncated")
	}
	return true, nil
}

// mcpReportReplacementsReady: every handler version with unfinished Tasks has
// at least one live worker per replacement on the candidate deployment, and no
// worker outside the release's deployments serves the namespace. The report
// lists only handler versions with unfinished Tasks, so an empty queue is ready;
// the adapter has already seen each replacement start from its instance logs.
func mcpReportReplacementsReady(report api.MCPTaskCompatibilityReportResponse, since time.Time, deployments mcpGateDeployments, replacements int) (bool, error) {
	if ok, err := mcpCompatibilityFresh(report, since); !ok || err != nil {
		return false, err
	}
	if deployments.Candidate == "" || replacements < 1 {
		return false, nil
	}
	for _, handler := range report.Handlers {
		var candidate int64
		for _, deployment := range handler.Deployments {
			switch {
			case deployment.DeploymentID == deployments.Candidate:
				candidate = deployment.Workers
			case !slices.Contains(deployments.Previous, deployment.DeploymentID):
				return false, errMCPGateOutsideWorkers
			}
		}
		if candidate < int64(replacements) {
			return false, nil
		}
	}
	return true, nil
}

// mcpReportPreviousGone: no handler version lists a previous deployment or a
// worker without a deployment identity.
func mcpReportPreviousGone(report api.MCPTaskCompatibilityReportResponse, since time.Time, deployments mcpGateDeployments) (bool, error) {
	if ok, err := mcpCompatibilityFresh(report, since); !ok || err != nil {
		return false, err
	}
	for _, handler := range report.Handlers {
		if handler.UnknownDeploymentWorkers > 0 {
			return false, nil
		}
		for _, deployment := range handler.Deployments {
			if slices.Contains(deployments.Previous, deployment.DeploymentID) {
				return false, nil
			}
		}
	}
	return true, nil
}

// reportMCPGateView reads the observer's report for the release's metric app
// and the release's deployments from its journal.
type reportMCPGateView struct {
	report      func(context.Context) (api.MCPTaskCompatibilityReportResponse, error)
	deployments func() (mcpGateDeployments, error)
}

func (v reportMCPGateView) read(ctx context.Context) (api.MCPTaskCompatibilityReportResponse, mcpGateDeployments, error) {
	deployments, err := v.deployments()
	if err != nil {
		return api.MCPTaskCompatibilityReportResponse{}, deployments, err
	}
	report, err := v.report(ctx)
	return report, deployments, err
}

func (v reportMCPGateView) Baseline(ctx context.Context) (time.Time, error) {
	report, err := v.report(ctx)
	if err != nil {
		return time.Time{}, err
	}
	if !report.Published || report.ObservedAt == nil {
		return time.Time{}, nil
	}
	return *report.ObservedAt, nil
}

func (v reportMCPGateView) Ready(ctx context.Context, since time.Time, replacements []string) (bool, error) {
	report, deployments, err := v.read(ctx)
	if err != nil {
		return false, nil // A failed read is retried until the release deadline.
	}
	return mcpReportReplacementsReady(report, since, deployments, len(replacements))
}

func (v reportMCPGateView) PreviousGone(ctx context.Context, since time.Time) (bool, error) {
	report, deployments, err := v.read(ctx)
	if err != nil {
		return false, nil
	}
	return mcpReportPreviousGone(report, since, deployments)
}

func mcpJournalDeployments(s mcpNativeReleaseState) mcpGateDeployments {
	previous := make([]string, 0, len(s.PreviousDeployments))
	for _, id := range s.PreviousDeployments {
		previous = append(previous, id)
	}
	slices.Sort(previous)
	return mcpGateDeployments{Candidate: s.WorkerDeployment, Previous: previous}
}

// runMCPBuiltinTaskGate runs the built-in gate for a Go or Python worker and
// returns the report as the Node gate prints it.
func runMCPBuiltinTaskGate(ctx context.Context, runtime string, p mcpNativeReleasePlan, state, fingerprint string, start, drain []string) []byte {
	failed := func(stage string) []byte {
		body, _ := json.Marshal(map[string]any{"ok": false, "executorSettled": true, "stage": stage, "detail": "Check the release plan, the web release command and the observer"})
		return body
	}
	namespace := strings.TrimSpace(os.Getenv("MCP_TASK_NAMESPACE"))
	if namespace == "" {
		return failed("configuration")
	}
	client, err := authedClient()
	if err != nil {
		return failed("configuration")
	}
	timeout := time.Duration(p.TimeoutSeconds) * time.Second
	hook := func(ctx context.Context, command []string, input map[string]any) ([]byte, error) {
		body, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		hookCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(hookCtx, command[0], command[1:]...) //nolint:gosec // The CLI's own adapter entrypoints.
		cmd.Env = append(environWithout(os.Environ(), "MCP_TASK_RELEASE_INPUT", "MCP_TASK_MIGRATION_DATABASE_URL"), "MCP_TASK_RELEASE_INPUT="+string(body))
		// The adapters are this CLI; their problem details go to the operator,
		// never into the gate report.
		cmd.Stderr = os.Stderr
		return cmd.Output()
	}
	view := reportMCPGateView{
		report: func(ctx context.Context) (api.MCPTaskCompatibilityReportResponse, error) {
			return client.GetAppMCPTaskCompatibilityReport(ctx, p.ObserverMetricApp)
		},
		deployments: func() (mcpGateDeployments, error) {
			s, err := loadMCPNativeState(state, fingerprint)
			return mcpJournalDeployments(s), err
		},
	}
	gate := &mcpGate{
		view: view, namespace: namespace, releaseID: fingerprint, timeout: timeout, poll: 2 * time.Second, checkpointPath: state + ".gate",
		migrate: func(context.Context) error { return requireMCPWebTaskMigration(p.WebPath, runtime) },
		start: func(ctx context.Context, input map[string]any) ([]string, error) {
			output, err := hook(ctx, start, input)
			if err != nil {
				return nil, err
			}
			var started struct {
				WorkerIDs []string `json:"workerIDs"`
			}
			if err := json.Unmarshal(output, &started); err != nil {
				return nil, err
			}
			return started.WorkerIDs, nil
		},
		drain: func(ctx context.Context, input map[string]any) error {
			_, err := hook(ctx, drain, input)
			return err
		},
	}
	body, _ := json.Marshal(gate.run(ctx))
	return body
}

// mcpBuiltinReplacementsReady is the Go/Python form of tasks-release-readiness.js,
// rechecked before promotion and each park.
func mcpBuiltinReplacementsReady(ctx context.Context, c *Client, p mcpNativeReleasePlan, s *mcpNativeReleaseState) error {
	report, err := c.GetAppMCPTaskCompatibilityReport(ctx, p.ObserverMetricApp)
	if err != nil {
		return err
	}
	ok, err := mcpReportReplacementsReady(report, time.Time{}, mcpJournalDeployments(*s), len(s.WorkerIDs))
	if err != nil {
		return err
	}
	if !ok || !validMCPWorkerIDs(s.WorkerIDs, false) {
		return errors.New("replacement workers are not ready in the observer's compatibility report")
	}
	return nil
}
