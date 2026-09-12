// Package executor runs one caller-supplied source unit inside the sanitized
// execution guest. It deliberately has no filesystem, network, or secret
// injection API: source and JSON input exist only in a per-request scratch
// directory and the interpreter is launched with a small, fixed environment.
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

const (
	guestUID         = 1000
	guestGID         = 1000
	resultReserve    = 4 // JSON null, used on all failure paths
	nodeSourceName   = "source.mjs"
	pythonSourceName = "source.py"
)

// commandBuilder is kept private so the production constructor cannot be
// accidentally replaced by a caller. Tests in this package use it to run a
// deterministic helper process without depending on a host Node/Python
// installation.
type commandBuilder func(context.Context, string, ...string) *exec.Cmd

// commandResolver resolves the fixed interpreter command for a validated
// request. It is kept as a narrow seam so tests can exercise deadline and
// teardown behaviour with a deterministic helper process.
type commandResolver func(executionproto.Request) (string, []string, string, error)

// Executor is the guest-side execution protocol handler.
type Executor struct {
	build   commandBuilder
	resolve commandResolver
	now     func() time.Time
}

// New returns the production executor. The returned handler is safe to use
// for exactly one request, as required by executionproto.Serve; the VM exits
// after that exchange and therefore no state is retained between callers.
func New() *Executor {
	e := &Executor{
		build: func(ctx context.Context, path string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, path, args...)
		},
		now: time.Now,
	}
	e.resolve = e.command
	return e
}

// Handle implements executionproto.Handler.
func (e *Executor) Handle(ctx context.Context, req executionproto.Request, stdout, stderr *executionproto.OutputWriter) (executionproto.Result, error) {
	if e == nil || e.build == nil {
		return executionproto.Result{}, errors.New("execution executor is not configured")
	}
	if err := req.Validate(); err != nil {
		return executionproto.Result{}, err
	}
	requestCtx, cancelRequest := context.WithTimeout(ctx, time.Duration(req.TimeoutMS)*time.Millisecond)
	defer cancelRequest()
	if err := requestCtx.Err(); err != nil {
		return executionproto.Result{}, err
	}
	resolve := e.resolve
	if resolve == nil {
		resolve = e.command
	}
	interpreter, args, sourceName, err := resolve(req)
	if err != nil {
		return executionproto.Result{}, err
	}

	started := e.now()
	workdir, err := newWorkdir()
	if err != nil {
		return executionproto.Result{}, errors.New("execution scratch space unavailable")
	}
	defer func() { _ = os.RemoveAll(workdir) }()

	sourcePath := filepath.Join(workdir, sourceName)
	inputPath := filepath.Join(workdir, "input.json")
	resultPath := filepath.Join(workdir, "result.json")
	if err := writeGuestFile(sourcePath, []byte(req.Source)); err != nil {
		return executionproto.Result{}, errors.New("execution source staging failed")
	}
	if err := writeGuestFile(inputPath, req.Input); err != nil {
		return executionproto.Result{}, errors.New("execution input staging failed")
	}
	if err := writeGuestFile(resultPath, nil); err != nil {
		return executionproto.Result{}, errors.New("execution result staging failed")
	}
	if err := requestCtx.Err(); err != nil {
		return executionproto.Result{}, err
	}

	maxResult := req.MaxOutput
	if maxResult > resultReserve {
		maxResult -= resultReserve
	}
	args = append(args, sourcePath, inputPath, resultPath, req.ExecutionID, string(req.Runtime), fmt.Sprint(maxResult))
	commandCtx, cancel := context.WithCancel(requestCtx)
	defer cancel()
	cmd := e.build(commandCtx, interpreter, args...)
	if cmd == nil {
		return executionproto.Result{}, errors.New("execution interpreter unavailable")
	}
	cmd.Env = guestEnv(req.Runtime)
	cmd.Dir = workdir
	configureProcess(cmd)

	budget := &streamBudget{limit: maxResult}
	cmd.Stdout = &budgetWriter{dst: stdout, budget: budget, cancel: cancel}
	cmd.Stderr = &budgetWriter{dst: stderr, budget: budget, cancel: cancel}
	if err := cmd.Start(); err != nil {
		return failedResult(started, e.now(), "guest_error"), nil
	}

	processDone := make(chan struct{})
	processStopped := make(chan struct{})
	go func() {
		defer close(processStopped)
		select {
		case <-commandCtx.Done():
			terminateProcess(cmd)
		case <-processDone:
		}
	}()
	runErr := cmd.Wait()
	close(processDone)
	<-processStopped

	if writerErr := budget.Err(); writerErr != nil {
		if errors.Is(writerErr, executionproto.ErrOutputLimitExceeded) {
			return failedResultWithUsage(started, e.now(), "output_limit", true, budget.Used()), nil
		}
		if errors.Is(writerErr, context.Canceled) && requestCtx.Err() == nil {
			return failedResult(started, e.now(), "guest_error"), nil
		}
	}
	if errors.Is(requestCtx.Err(), context.DeadlineExceeded) || errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
		return executionproto.Result{}, context.DeadlineExceeded
	}
	if errors.Is(requestCtx.Err(), context.Canceled) {
		return executionproto.Result{}, context.Canceled
	}
	if runErr != nil {
		// Interpreter syntax/runtime failures are caller-visible only as the
		// fixed guest_error code. Their diagnostic text remains on stderr.
		return failedResultWithUsage(started, e.now(), "guest_error", false, budget.Used()), nil
	}

	result, err := readResult(resultPath)
	if err != nil {
		return failedResultWithUsage(started, e.now(), "guest_error", false, budget.Used()), nil
	}
	if len(result)+budget.Used() > req.MaxOutput {
		return failedResultWithUsage(started, e.now(), "output_limit", true, budget.Used()), nil
	}
	return executionproto.Result{
		Status: api.ExecutionStatusSucceeded,
		Result: result,
		Usage:  api.ExecutionUsage{WallTimeMS: elapsedMS(started, e.now())},
	}, nil
}

func (e *Executor) command(req executionproto.Request) (string, []string, string, error) {
	switch req.Runtime {
	case api.ExecutionRuntimeNode22, api.ExecutionRuntimeNode24:
		path, err := lookupInterpreter("node", "/usr/local/bin/node")
		return path, []string{"--input-type=module", "-e", nodeWrapper}, nodeSourceName, err
	case api.ExecutionRuntimePython312, api.ExecutionRuntimePython313:
		path, err := lookupInterpreter("python3", "/usr/local/bin/python3")
		return path, []string{"-I", "-S", "-c", pythonWrapper}, pythonSourceName, err
	default:
		return "", nil, "", fmt.Errorf("unsupported execution runtime %q", req.Runtime)
	}
}

func lookupInterpreter(name, fallback string) (string, error) {
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	if _, err := os.Stat(fallback); err == nil {
		return fallback, nil
	}
	return "", fmt.Errorf("execution interpreter %s is unavailable", name)
}

func newWorkdir() (string, error) {
	dir, err := os.MkdirTemp("", "faas-execution-")
	if err != nil {
		return "", err
	}
	// The directory is owned by the runtime identity in production. A local
	// developer running the package as a non-root user cannot chown it; the
	// fallback keeps hermetic tests usable without weakening guest images.
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	if err := os.Chown(dir, guestUID, guestGID); err != nil {
		if err := os.Chmod(dir, 0o755); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
	}
	return dir, nil
}

func writeGuestFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	if err := os.Chown(path, guestUID, guestGID); err != nil {
		// If we are not PID 1/root, retain a readable mode for the current test
		// user. In the production guest chown succeeds and the file stays 0600.
		if err := os.Chmod(path, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func readResult(path string) (json.RawMessage, error) {
	//nolint:forbidigo // path is a freshly-created per-request scratch file; no customer path is accepted here.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, int64(executionproto.MaxFrameBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > executionproto.MaxFrameBytes || !json.Valid(data) {
		return nil, errors.New("invalid execution result")
	}
	return json.RawMessage(data), nil
}

func guestEnv(runtime api.ExecutionRuntime) []string {
	return []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=/tmp",
		"LANG=C.UTF-8",
		"FAAS_RUNTIME=" + string(runtime),
	}
}

func elapsedMS(start, end time.Time) int64 {
	if end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func failedResult(start, end time.Time, code string) executionproto.Result {
	return failedResultWithUsage(start, end, code, false, 0)
}

func failedResultWithUsage(start, end time.Time, code string, truncated bool, _ int) executionproto.Result {
	return executionproto.Result{
		Status:          api.ExecutionStatusFailed,
		Result:          json.RawMessage("null"),
		OutputTruncated: truncated,
		FailureCode:     code,
		Usage:           api.ExecutionUsage{WallTimeMS: elapsedMS(start, end)},
	}
}

type streamBudget struct {
	mu    sync.Mutex
	limit int
	used  int
	err   error
}

func (b *streamBudget) reserve(n int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - b.used
	if remaining <= 0 {
		b.err = executionproto.ErrOutputLimitExceeded
		return 0
	}
	if n > remaining {
		n = remaining
		b.err = executionproto.ErrOutputLimitExceeded
	}
	b.used += n
	return n
}

func (b *streamBudget) Used() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

func (b *streamBudget) Err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

type budgetWriter struct {
	dst    io.Writer
	budget *streamBudget
	cancel context.CancelFunc
}

func (w *budgetWriter) Write(p []byte) (int, error) {
	if w == nil || w.dst == nil || w.budget == nil {
		return 0, errors.New("nil execution output writer")
	}
	n := w.budget.reserve(len(p))
	if n == 0 {
		if w.cancel != nil {
			w.cancel()
		}
		return 0, executionproto.ErrOutputLimitExceeded
	}
	written, err := w.dst.Write(p[:n])
	if err != nil {
		w.budget.mu.Lock()
		w.budget.err = err
		w.budget.mu.Unlock()
		if w.cancel != nil {
			w.cancel()
		}
		return written, err
	}
	if n < len(p) {
		if w.cancel != nil {
			w.cancel()
		}
		return written, executionproto.ErrOutputLimitExceeded
	}
	return written, nil
}

const nodeWrapper = `
import fs from "node:fs";
import { pathToFileURL } from "node:url";
const [sourcePath, inputPath, resultPath, executionID, runtime, maxBytes] = process.argv.slice(1);
const moduleURL = pathToFileURL(sourcePath).href + "?execution=" + encodeURIComponent(executionID);
const loaded = await import(moduleURL);
if (typeof loaded.default !== "function") throw new Error("default export must be a function");
const input = JSON.parse(fs.readFileSync(inputPath, "utf8"));
const context = Object.freeze({ execution_id: executionID, runtime });
let value = await loaded.default(input, context);
if (value === undefined) value = null;
const encoded = JSON.stringify(value);
if (encoded === undefined) throw new Error("result is not JSON serializable");
if (Buffer.byteLength(encoded, "utf8") > Number(maxBytes)) throw new Error("result exceeds output budget");
fs.writeFileSync(resultPath, encoded, { encoding: "utf8", mode: 0o600 });
`

const pythonWrapper = `
import asyncio, importlib.util, inspect, json, sys
source_path, input_path, result_path, execution_id, runtime, max_bytes = sys.argv[1:]
spec = importlib.util.spec_from_file_location("faas_execution", source_path)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
handler = getattr(module, "main", None)
if not callable(handler): raise RuntimeError("main must be callable")
with open(input_path, encoding="utf-8") as input_file:
    value = handler(json.load(input_file), {"execution_id": execution_id, "runtime": runtime})
if inspect.isawaitable(value): value = asyncio.run(value)
encoded = json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False)
encoded_bytes = encoded.encode("utf-8")
if len(encoded_bytes) > int(max_bytes): raise RuntimeError("result exceeds output budget")
with open(result_path, "wb") as result_file: result_file.write(encoded_bytes)
`
