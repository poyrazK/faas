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
	"sync"
	"time"

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
	// answer: the driver interrupts the kernel, then restarts it.
	sandboxCodeRunGrace = 30 * time.Second
	// sandboxCodeEventMaxBytes bounds one event line from the driver.
	sandboxCodeEventMaxBytes = sandboxproto.MaxOutputBytes + 64<<10
)

// codeDriver is the session's driver process, started on first use and
// again if it exits.
type codeDriver struct {
	mu         sync.Mutex
	path       string
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
	spec := sandboxCommandSpec{argv: []string{d.path, "--socket", d.socket(), "--workspace", s.workspace},
		env: sandboxEnv(s.home, "app", false, s.commandEnv(nil)), dir: s.workspace}
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
	err := s.codeCall(ctx, map[string]any{"op": "run", "context": req.Context, "code": req.Code,
		"timeout_ms": req.TimeoutMillis, "max_output_bytes": req.MaxOutputBytes}, timeout, func(ev codeEvent) error {
		switch ev.Type {
		case "stdout":
			_, _ = call.Stdout().Write([]byte(ev.Text))
		case "stderr":
			_, _ = call.Stderr().Write([]byte(ev.Text))
		case "result":
			res.Results = append(res.Results, sandboxproto.CodeOutput{Data: ev.Data, IsMainResult: ev.IsMainResult})
		case "error":
			res.Error = &sandboxproto.CodeError{Name: ev.Name, Value: ev.Value, Traceback: ev.Traceback}
		case "done":
			res.ExecutionCount, res.TimedOut, res.OutputTruncated, done = ev.ExecutionCount, ev.TimedOut, ev.Truncated, true
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
	body := map[string]any{"op": req.Action, "context": req.Context}
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
