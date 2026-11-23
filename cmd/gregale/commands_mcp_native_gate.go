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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

// The built-in Task release gate (ADR-20261123160000) releases Go and Python
// Tasks workers. The Node starter ships its own gate (tasks-release.js); the Go
// and Python starters share the worker registry but have no admission tables,
// so the CLI checks what they do record: each replacement worker must register
// every handler version that unfinished Tasks need before previous workers are
// drained. It drives the same start/drain adapters and writes the same
// checkpoint as the Node gate, and shares its namespace advisory lock.

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

// requireMCPNativeNodeWorker guards the recovery, quarantine and retirement
// flows: they need a database-side claim fence and the Node gate's restore
// scripts, which the Go and Python starters do not have.
func requireMCPNativeNodeWorker(workerPath string) error {
	runtime, err := mcpNativeWorkerRuntime(workerPath)
	if err != nil {
		return err
	}
	if runtime != mcpWorkerRuntimeNode {
		return fmt.Errorf("this step needs a Node worker (mcp-node-tasks); a %s worker supports `release run` and `release status` only, so recover or retire it with the manual rollout in docs/mcp.md", runtime)
	}
	return nil
}

// mcpTaskMigrationCommand runs the starter's own schema migration from the
// worker source, as the Node gate runs its store migration.
func mcpTaskMigrationCommand(ctx context.Context, runtime, workerPath string) *exec.Cmd {
	var command *exec.Cmd
	if runtime == mcpWorkerRuntimeGo {
		command = exec.CommandContext(ctx, "go", "run", ".", "tasks-migrate", "apply")
	} else {
		command = exec.CommandContext(ctx, "python3", "task_schema.py", "apply")
	}
	command.Dir = workerPath
	return command
}

type mcpGateWorker struct {
	ID        string
	Handlers  []mcpTaskHandlerRef
	Draining  bool
	Heartbeat time.Time
	Fresh     bool
}

type mcpTaskHandlerRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// mcpGateStore is the gate's view of the Task database.
type mcpGateStore interface {
	TryLock(ctx context.Context, namespace string) (bool, error)
	Unlock(ctx context.Context, namespace string) error
	Now(ctx context.Context) (time.Time, error)
	Workers(ctx context.Context, namespace string) ([]mcpGateWorker, error)
	// PendingHandlers lists the handler versions unfinished Tasks need.
	PendingHandlers(ctx context.Context, namespace string) ([]mcpTaskHandlerRef, error)
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
	store          mcpGateStore
	namespace      string
	releaseID      string
	timeout        time.Duration
	poll           time.Duration
	checkpointPath string
	migrate        func(ctx context.Context) error
	start          func(ctx context.Context, input map[string]any) ([]string, error)
	drain          func(ctx context.Context, input map[string]any) error
}

// run mirrors releaseMcpTasks in the Node starter. Errors never reach the
// report: hook and database errors can carry credentials.
func (g *mcpGate) run(ctx context.Context) mcpGateReport {
	report := mcpGateReport{ExecutorSettled: true, Stage: "release_lock"}
	acquired, err := g.store.TryLock(ctx, g.namespace)
	if err != nil || !acquired {
		return report
	}
	defer func() { _ = g.store.Unlock(context.WithoutCancel(ctx), g.namespace) }()
	report.Stage = "checkpoint"
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
	var previous []string
	var since time.Time
	if checkpoint != nil {
		previous = checkpoint.PreviousWorkerIDs
		if since, err = time.Parse(time.RFC3339Nano, checkpoint.StartedAt); err != nil {
			return report
		}
	} else {
		workers, err := g.store.Workers(ctx, g.namespace)
		if err != nil {
			return report
		}
		for _, worker := range workers {
			previous = append(previous, worker.ID)
		}
		if since, err = g.store.Now(ctx); err != nil {
			return report
		}
	}
	record := mcpGateCheckpoint{Version: 1, Namespace: g.namespace, ReleaseID: g.releaseID, PreviousWorkerIDs: previous, StartedAt: since.UTC().Format(time.RFC3339Nano)}
	if previous == nil {
		record.PreviousWorkerIDs = []string{}
	}
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
	if !validMCPWorkerIDs(ids, false) || slices.ContainsFunc(ids, func(id string) bool { return slices.Contains(previous, id) }) {
		return report
	}
	record.ReplacementWorkerIDs = ids
	if (checkpoint == nil || checkpoint.ReplacementWorkerIDs == nil) && g.saveCheckpoint(record) != nil {
		return report
	}
	report.Stage = "replacement_readiness"
	ready := func(ctx context.Context) (bool, error) { return g.ready(ctx, ids, since) }
	if checkpoint != nil && checkpoint.Completed {
		ok, err := ready(ctx)
		if err != nil || !ok || g.anyPresent(ctx, previous) {
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
	if g.wait(ctx, func(ctx context.Context) (bool, error) {
		ok, err := ready(ctx)
		if err != nil || !ok {
			return false, errors.New("replacement readiness lost")
		}
		return !g.anyPresent(ctx, previous), nil
	}) != nil {
		return report
	}
	report.Stage = "checkpoint_complete"
	record.Completed = true
	if g.saveCheckpoint(record) != nil {
		return report
	}
	return mcpGateReport{OK: true, ExecutorSettled: true, Stage: "complete", ReplacementWorkerIDs: ids}
}

// ready: every replacement is registered, live, heartbeating since the
// release began and serves every handler version unfinished Tasks need.
func (g *mcpGate) ready(ctx context.Context, ids []string, since time.Time) (bool, error) {
	workers, err := g.store.Workers(ctx, g.namespace)
	if err != nil {
		return false, err
	}
	pending, err := g.store.PendingHandlers(ctx, g.namespace)
	if err != nil {
		return false, err
	}
	return mcpReplacementsReady(workers, pending, ids, since), nil
}

func mcpReplacementsReady(workers []mcpGateWorker, pending []mcpTaskHandlerRef, ids []string, since time.Time) bool {
	for _, id := range ids {
		index := slices.IndexFunc(workers, func(worker mcpGateWorker) bool { return worker.ID == id })
		if index < 0 {
			return false
		}
		worker := workers[index]
		if worker.Draining || !worker.Fresh || worker.Heartbeat.Before(since) || len(worker.Handlers) == 0 {
			return false
		}
		for _, need := range pending {
			if !slices.Contains(worker.Handlers, need) {
				return false
			}
		}
	}
	return true
}

func (g *mcpGate) anyPresent(ctx context.Context, ids []string) bool {
	workers, err := g.store.Workers(ctx, g.namespace)
	if err != nil {
		return true
	}
	return slices.ContainsFunc(workers, func(worker mcpGateWorker) bool { return slices.Contains(ids, worker.ID) })
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

// pgMCPGateStore reads the worker registry the Go and Python starters write.
type pgMCPGateStore struct {
	pool *pgxpool.Pool
	lock *pgxpool.Conn
}

func mcpGateLockKey(namespace string) string { return "gregale_mcp_release:" + namespace }

func (s *pgMCPGateStore) TryLock(ctx context.Context, namespace string) (bool, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, mcpGateLockKey(namespace)).Scan(&acquired); err != nil || !acquired {
		conn.Release()
		return false, err
	}
	s.lock = conn
	return true, nil
}

func (s *pgMCPGateStore) Unlock(ctx context.Context, namespace string) error {
	if s.lock == nil {
		return nil
	}
	defer func() { s.lock.Release(); s.lock = nil }()
	_, err := s.lock.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, mcpGateLockKey(namespace))
	return err
}

func (s *pgMCPGateStore) Now(ctx context.Context) (time.Time, error) {
	var now time.Time
	err := s.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now, err
}

func (s *pgMCPGateStore) Workers(ctx context.Context, namespace string) ([]mcpGateWorker, error) {
	rows, err := s.pool.Query(ctx, `SELECT worker_id::text, handlers, draining, heartbeat_at,
		heartbeat_at > clock_timestamp() - interval '45 seconds'
		FROM gregale_mcp_task_workers WHERE namespace = $1 AND expires_at > clock_timestamp()`, namespace)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (mcpGateWorker, error) {
		var worker mcpGateWorker
		var handlers []byte
		if err := row.Scan(&worker.ID, &handlers, &worker.Draining, &worker.Heartbeat, &worker.Fresh); err != nil {
			return worker, err
		}
		return worker, json.Unmarshal(handlers, &worker.Handlers)
	})
}

func (s *pgMCPGateStore) PendingHandlers(ctx context.Context, namespace string) ([]mcpTaskHandlerRef, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT tool_name, handler_version FROM gregale_mcp_tasks
		WHERE namespace = $1 AND expires_at > clock_timestamp() AND status IN ('queued','running','input_required')`, namespace)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (mcpTaskHandlerRef, error) {
		var ref mcpTaskHandlerRef
		return ref, row.Scan(&ref.Name, &ref.Version)
	})
}

// mcpNativeTaskDatabase opens the worker's Task binding as the Node gate does:
// the namespace from MCP_TASK_NAMESPACE and the URL from the worker config's
// database_url_env (DATABASE_URL by default).
func mcpNativeTaskDatabase(ctx context.Context, workerPath string, maxConns int32) (*pgxpool.Pool, string, error) {
	namespace := strings.TrimSpace(os.Getenv("MCP_TASK_NAMESPACE"))
	if namespace == "" {
		return nil, "", errors.New("set MCP_TASK_NAMESPACE to the release's Task namespace")
	}
	cfg, err := mcphosting.Load(workerPath)
	if err != nil {
		return nil, "", err
	}
	env := "DATABASE_URL"
	if cfg.Tasks != nil && cfg.Tasks.DatabaseURLEnv != "" {
		env = cfg.Tasks.DatabaseURLEnv
	}
	url := os.Getenv(env)
	if url == "" {
		return nil, "", fmt.Errorf("set %s to the worker's Task database binding", env)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, "", errors.New("invalid Task database binding")
	}
	config.MaxConns = maxConns
	config.ConnConfig.ConnectTimeout = 5 * time.Second
	config.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, "", errors.New("could not open the Task database")
	}
	return pool, namespace, nil
}

// runMCPBuiltinTaskGate runs the built-in gate for a Go or Python worker and
// returns the report as the Node gate prints it.
func runMCPBuiltinTaskGate(ctx context.Context, runtime, workerPath, releaseID, checkpointPath string, timeout time.Duration, start, drain []string) []byte {
	failed := func(stage string) []byte {
		body, _ := json.Marshal(map[string]any{"ok": false, "executorSettled": true, "stage": stage, "detail": "Check release plan, Task bindings, and migration credentials"})
		return body
	}
	if os.Getenv("MCP_TASK_MIGRATION_DATABASE_URL") == "" {
		return failed("configuration")
	}
	pool, namespace, err := mcpNativeTaskDatabase(ctx, workerPath, 4)
	if err != nil {
		return failed("configuration")
	}
	defer pool.Close()
	hook := func(ctx context.Context, command []string, input map[string]any) ([]byte, error) {
		body, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		hookCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(hookCtx, command[0], command[1:]...) //nolint:gosec // The CLI's own adapter entrypoints.
		cmd.Env = append(environWithout(os.Environ(), "MCP_TASK_RELEASE_INPUT", "MCP_TASK_MIGRATION_DATABASE_URL"), "MCP_TASK_RELEASE_INPUT="+string(body))
		return cmd.Output()
	}
	gate := &mcpGate{
		store: &pgMCPGateStore{pool: pool}, namespace: namespace, releaseID: releaseID, timeout: timeout, poll: 250 * time.Millisecond, checkpointPath: checkpointPath,
		migrate: func(ctx context.Context) error {
			migrateCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			return mcpTaskMigrationCommand(migrateCtx, runtime, workerPath).Run()
		},
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
func mcpBuiltinReplacementsReady(ctx context.Context, workerPath string, ids []string) error {
	pool, namespace, err := mcpNativeTaskDatabase(ctx, workerPath, 1)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := &pgMCPGateStore{pool: pool}
	workers, err := store.Workers(ctx, namespace)
	if err != nil {
		return err
	}
	pending, err := store.PendingHandlers(ctx, namespace)
	if err != nil {
		return err
	}
	if !validMCPWorkerIDs(ids, false) || !mcpReplacementsReady(workers, pending, ids, time.Time{}) {
		return errors.New("replacement database heartbeats are not ready")
	}
	return nil
}
