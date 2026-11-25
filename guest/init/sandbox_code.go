package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

// The code interpreter (ADR-20261027090013 step 14). An image that carries
// a driver at sandboxCodeDriver (the code-interpreter preset) runs code in
// Jupyter kernels: guest-init starts the driver on first use, as the
// session's user, and relays code_run and code_context to its Unix socket.
// An image without the driver answers unsupported.
const (
	sandboxCodeDriver     = "/opt/gregale/code-interpreter/driver.py"
	sandboxCodeRuntimeDir = "/run/gregale-code"
	// sandboxCodeStartTimeout is how long the driver has to listen.
	sandboxCodeStartTimeout = 30 * time.Second
	// sandboxCodeRunGrace is how long past its timeout a run may take to
	// answer: starting the default kernel on first use (up to 60 s), then
	// past the timeout an interrupt (5 s) and a restart (up to 60 s).
	sandboxCodeRunGrace = 150 * time.Second
	// sandboxCodeResultMaxBytes bounds a run's rich results as this guest
	// encodes them, so the result always fits one control frame beside a
	// traceback; results past it are dropped and the run says so.
	sandboxCodeResultMaxBytes = 3 << 20
	// sandboxCodeEventMaxBytes bounds one event line from the driver.
	sandboxCodeEventMaxBytes = sandboxproto.MaxOutputBytes + 64<<10
)

// codeDriver is the session's driver process, started on first use and
// again if it exits.
type codeDriver struct {
	mu   sync.Mutex
	path string
	// python, when set, runs the driver instead of its shebang (tests).
	python     string
	runtimeDir string
	proc       sandboxProcess
	exited     chan struct{}
}

func (d *codeDriver) socket() string { return filepath.Join(d.runtimeDir, "driver.sock") }

// dial connects to the driver, starting it first when it does not run.
func (s *sandboxServer) codeDial(ctx context.Context) (net.Conn, error) {
	d := s.code
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.proc != nil {
		select {
		case <-d.exited:
			d.proc = nil
		default:
			var dialer net.Dialer
			if conn, err := dialer.DialContext(ctx, "unix", d.socket()); err == nil {
				return conn, nil
			}
		}
	}
	if d.proc == nil {
		if err := s.startCodeDriver(); err != nil {
			return nil, err
		}
	}
	deadline := time.Now().Add(sandboxCodeStartTimeout)
	for {
		var dialer net.Dialer
		conn, err := dialer.DialContext(ctx, "unix", d.socket())
		if err == nil {
			return conn, nil
		}
		select {
		case <-d.exited:
			d.proc = nil
			return nil, sandboxError(sandboxproto.CodeStartFailed, "the code interpreter exited while starting")
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return nil, sandboxError(sandboxproto.CodeStartFailed, "the code interpreter did not start within %s", sandboxCodeStartTimeout)
		}
	}
}

// startCodeDriver runs the driver as the session's user with the session's
// environment. Its own output goes to driver.log beside its socket.
func (s *sandboxServer) startCodeDriver() error {
	d := s.code
	//nolint:forbidigo // a fixed guest path the code-interpreter image provides
	if _, err := os.Stat(d.path); err != nil {
		return sandboxError(sandboxproto.CodeUnsupported, "this sandbox has no code interpreter; create it with the code-interpreter template")
	}
	//nolint:forbidigo // guest-init owns the driver's runtime directory
	if err := os.MkdirAll(d.runtimeDir, 0o700); err != nil {
		return sandboxError(sandboxproto.CodeInternal, "make the code interpreter's directory: %v", err)
	}
	if s.owner >= 0 {
		//nolint:forbidigo // guest-init owns the driver's runtime directory
		if err := os.Chown(d.runtimeDir, s.owner, s.owner); err != nil {
			return sandboxError(sandboxproto.CodeInternal, "own the code interpreter's directory: %v", err)
		}
	}
	// The driver runs with the base environment only: the session's
	// environment comes with every request, so its kernels follow changes.
	argv := []string{d.path, "--socket", d.socket(), "--workspace", s.workspace}
	if d.python != "" {
		argv = append([]string{d.python}, argv...)
	}
	spec := sandboxCommandSpec{argv: argv, env: sandboxEnv(s.home, "app", false, nil), dir: s.workspace}
	proc, err := s.runner.start(spec)
	if err != nil {
		return sandboxError(sandboxproto.CodeStartFailed, "start the code interpreter: %s", err.Error())
	}
	//nolint:forbidigo // the driver's log, beside its socket
	logFile, err := os.OpenFile(filepath.Join(d.runtimeDir, "driver.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		logFile = nil
	}
	out := io.Discard
	if logFile != nil {
		out = logFile
	}
	var pumps sync.WaitGroup
	for _, r := range []io.Reader{proc.stdout(), proc.stderr()} {
		if r == nil {
			continue
		}
		pumps.Add(1)
		go func() {
			defer pumps.Done()
			_, _ = io.Copy(out, r)
		}()
	}
	exited := make(chan struct{})
	go func() {
		_ = proc.wait()
		// Kernels run in sessions of their own: a dead driver's kernels
		// would keep their memory beside the next driver's.
		stopCodeKernels(d.runtimeDir)
		pumps.Wait()
		proc.closeOutput()
		if logFile != nil {
			_ = logFile.Close()
		}
		close(exited)
	}()
	d.proc, d.exited = proc, exited
	return nil
}

// stopCodeKernels kills the kernels a driver recorded in kernel-<id>.pid,
// each with its process group, and removes the records. A pid is killed
// only while its command line is still an ipykernel's.
func stopCodeKernels(runtimeDir string) {
	files, _ := filepath.Glob(filepath.Join(runtimeDir, "kernel-*.pid"))
	for _, file := range files {
		raw, err := os.ReadFile(file) //nolint:forbidigo // the driver's own record in its runtime directory
		_ = os.Remove(file)           //nolint:forbidigo // the driver's own record in its runtime directory
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || pid <= 1 {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline")) //nolint:forbidigo // procfs
		if err != nil || !strings.Contains(string(cmdline), "ipykernel") {
			continue
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// codeEvent is one line the driver answers with.
type codeEvent struct {
	Type           string                     `json:"type"`
	Text           string                     `json:"text"`
	Data           map[string]json.RawMessage `json:"data"`
	IsMainResult   bool                       `json:"is_main_result"`
	Name           string                     `json:"name"`
	Value          string                     `json:"value"`
	Traceback      string                     `json:"traceback"`
	ExecutionCount *int                       `json:"execution_count"`
	TimedOut       bool                       `json:"timed_out"`
	Truncated      bool                       `json:"output_truncated"`
	Code           string                     `json:"code"`
	Message        string                     `json:"message"`
	ID             string                     `json:"id"`
	Language       string                     `json:"language"`
	Cwd            string                     `json:"cwd"`
	Contexts       []sandboxproto.CodeContext `json:"contexts"`
}

// codeCall sends one request to the driver and hands each event line to
// onEvent until the connection ends. A fail event is returned as the
// guest's refusal.
func (s *sandboxServer) codeCall(ctx context.Context, req map[string]any, timeout time.Duration, onEvent func(codeEvent) error) error {
	conn, err := s.codeDial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	line, err := json.Marshal(req)
	if err != nil {
		return sandboxError(sandboxproto.CodeInternal, "encode the request: %v", err)
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return sandboxError(sandboxproto.CodeInternal, "the code interpreter closed: %v", err)
	}
	r := bufio.NewReaderSize(conn, 64<<10)
	for {
		raw, err := readEventLine(r)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return sandboxError(sandboxproto.CodeInternal, "read the code interpreter: %v", err)
		}
		var ev codeEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			return sandboxError(sandboxproto.CodeInternal, "the code interpreter sent an unreadable event")
		}
		if ev.Type == "fail" {
			return sandboxError(driverCode(ev.Code), "%s", ev.Message)
		}
		if err := onEvent(ev); err != nil {
			return err
		}
	}
}

func readEventLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > sandboxCodeEventMaxBytes {
			return nil, errors.New("an event is too large")
		}
		if err == nil {
			return line, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				return line, nil
			}
			return nil, err
		}
	}
}

func driverCode(code string) string {
	switch code {
	case "not_found":
		return sandboxproto.CodeNotFound
	case "limit":
		return sandboxproto.CodeLimit
	case "unsupported":
		return sandboxproto.CodeUnsupported
	case "invalid_request":
		return sandboxproto.CodeInvalidRequest
	case "too_large":
		return sandboxproto.CodeTooLarge
	}
	return sandboxproto.CodeInternal
}

// codeRun runs code in a context, streaming its stdout and stderr.
func (s *sandboxServer) codeRun(ctx context.Context, req sandboxproto.Request, call *sandboxproto.Call) (any, error) {
	res := sandboxproto.CodeRunResult{Results: []sandboxproto.CodeOutput{}}
	done := false
	timeout := time.Duration(req.TimeoutMillis)*time.Millisecond + sandboxCodeRunGrace
	resultBytes := 0
	err := s.codeCall(ctx, s.codeRequest(map[string]any{"op": "run", "context": req.Context, "code": req.Code,
		"timeout_ms": req.TimeoutMillis, "max_output_bytes": req.MaxOutputBytes}), timeout, func(ev codeEvent) error {
		switch ev.Type {
		case "stdout":
			_, _ = call.Stdout().Write([]byte(ev.Text))
		case "stderr":
			_, _ = call.Stderr().Write([]byte(ev.Text))
		case "result":
			out := sandboxproto.CodeOutput{Data: ev.Data, IsMainResult: ev.IsMainResult}
			// A result that cannot be encoded or does not fit is dropped and
			// reported, like output past the run's cap.
			if encoded, err := json.Marshal(out); err == nil && resultBytes+len(encoded) <= sandboxCodeResultMaxBytes {
				resultBytes += len(encoded)
				res.Results = append(res.Results, out)
			} else {
				res.OutputTruncated = true
			}
		case "error":
			res.Error = &sandboxproto.CodeError{Name: ev.Name, Value: ev.Value, Traceback: ev.Traceback}
		case "done":
			res.ExecutionCount, res.TimedOut, done = ev.ExecutionCount, ev.TimedOut, true
			res.OutputTruncated = res.OutputTruncated || ev.Truncated
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, sandboxError(sandboxproto.CodeInternal, "the code interpreter ended the run without a result")
	}
	return res, nil
}

// codeContext creates, lists, restarts or removes a code context.
func (s *sandboxServer) codeContext(ctx context.Context, req sandboxproto.Request) (any, error) {
	body := s.codeRequest(map[string]any{"op": req.Action, "context": req.Context})
	if req.Action == sandboxproto.CodeActionCreate {
		cwd := req.Workdir
		if cwd != "" && !filepath.IsAbs(cwd) {
			cwd = filepath.Join(s.workspace, cwd)
		}
		body["language"], body["cwd"] = req.Language, cwd
	}
	var res sandboxproto.CodeContextResult
	err := s.codeCall(ctx, body, sandboxCodeStartTimeout+sandboxCodeRunGrace, func(ev codeEvent) error {
		switch ev.Type {
		case "context":
			res.Context = &sandboxproto.CodeContext{ID: ev.ID, Language: ev.Language, Cwd: ev.Cwd}
		case "contexts":
			res.Contexts = ev.Contexts
			if res.Contexts == nil {
				res.Contexts = []sandboxproto.CodeContext{}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// codeRequest adds what every driver request carries: the session's
// environment as it is now, and the session's cap on code contexts.
func (s *sandboxServer) codeRequest(body map[string]any) map[string]any {
	env := s.commandEnv(nil)
	if env == nil {
		env = map[string]string{}
	}
	body["env"], body["max_contexts"] = env, api.SandboxCodeContextsMax
	return body
}
