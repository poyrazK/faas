// Package workerpool keeps generated function adapters alive between
// invocations. Legacy protocol handlers remain one-process-per-request and are
// selected automatically when the adapter marker is absent.
package workerpool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

const protocolMarker = "FAAS_PERSISTENT_PROTOCOL_V1"

// Keep active interpreters within the same four-worker footprint previously
// used for idle retention. Waiters keep their invocation cancellation budget.
const maxWorkers = api.FunctionInterpreterMaxWorkers

// Spec identifies one generated adapter process pool.
type Spec struct {
	Executable  string
	Args        []string
	Env         []string
	HandlerPath string
}

var (
	supportCache sync.Map // handler path -> bool
	pools        sync.Map // command identity -> *pool
)

// InvokeIfSupported invokes a marked, newline-framed adapter through a reused
// worker. handled=false asks the caller to retain the legacy one-shot path.
func InvokeIfSupported(ctx context.Context, spec Spec, request, response any) (handled bool, err error) {
	if !supportsPersistentProtocol(spec.HandlerPath) {
		return false, nil
	}
	return true, poolFor(spec).invoke(ctx, request, response)
}

// PrewarmIfSupported starts and fully initializes one generated adapter before
// the runner advertises readiness. The idle process is then included in the
// init snapshot, so a restored VM does not pay interpreter and module-import
// startup on its first request.
func PrewarmIfSupported(ctx context.Context, spec Spec) (handled bool, err error) {
	if !supportsPersistentProtocol(spec.HandlerPath) {
		return false, nil
	}
	p := poolFor(spec)
	w, err := p.acquire(ctx)
	if err != nil {
		return true, err
	}
	p.release(w, true)
	return true, nil
}

func poolFor(spec Spec) *pool {
	key := strings.Join(append([]string{spec.Executable}, spec.Args...), "\x00")
	value, _ := pools.LoadOrStore(key, newPool(spec))
	return value.(*pool)
}

func supportsPersistentProtocol(path string) bool {
	if cached, ok := supportCache.Load(path); ok {
		return cached.(bool)
	}
	f, err := os.Open(path) //nolint:forbidigo // HandlerPath is the runner-owned generated adapter path; this reads only its protocol marker.
	if err != nil {
		supportCache.Store(path, false)
		return false
	}
	defer func() { _ = f.Close() }()
	prefix, err := io.ReadAll(io.LimitReader(f, 4096))
	supported := err == nil && strings.Contains(string(prefix), protocolMarker)
	supportCache.Store(path, supported)
	return supported
}

type pool struct {
	spec    Spec
	mu      sync.Mutex
	idle    []*worker
	live    int
	changed chan struct{}
}

func newPool(spec Spec) *pool { return &pool{spec: spec, changed: make(chan struct{})} }

func (p *pool) acquire(ctx context.Context) (*worker, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p.mu.Lock()
		if n := len(p.idle); n > 0 {
			w := p.idle[n-1]
			p.idle = p.idle[:n-1]
			p.mu.Unlock()
			return w, nil
		}
		if p.live < maxWorkers {
			p.live++ // Reserve before starting: concurrent misses cannot oversubscribe.
			p.mu.Unlock()
			w, err := startWorker(ctx, p.spec)
			if err != nil {
				p.mu.Lock()
				p.live--
				p.notifyLocked()
				p.mu.Unlock()
			}
			return w, err
		}
		changed := p.changed
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (p *pool) notifyLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
}

func (p *pool) release(w *worker, healthy bool) {
	if !healthy {
		w.close()
	}
	p.mu.Lock()
	if healthy {
		p.idle = append(p.idle, w)
	} else {
		p.live--
	}
	p.notifyLocked()
	p.mu.Unlock()
}

func (p *pool) invoke(ctx context.Context, request, response any) error {
	w, err := p.acquire(ctx)
	if err != nil {
		return err
	}
	healthy := false
	defer func() { p.release(w, healthy) }()

	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("workerpool: encode request: %w", err)
	}
	payload = append(payload, '\n')
	if _, err := w.stdin.Write(payload); err != nil {
		return fmt.Errorf("workerpool: write request: %w", err)
	}

	type readResult struct {
		line []byte
		err  error
	}
	result := make(chan readResult, 1)
	go func() {
		line, readErr := w.stdout.ReadBytes('\n')
		result <- readResult{line: line, err: readErr}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case got := <-result:
		if got.err != nil {
			return fmt.Errorf("workerpool: read response: %w", got.err)
		}
		if err := json.Unmarshal(got.line, response); err != nil {
			return fmt.Errorf("workerpool: decode response: %w", err)
		}
		healthy = true
		return nil
	}
}

type worker struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	once   sync.Once
}

func startWorker(ctx context.Context, spec Spec) (*worker, error) {
	if spec.Executable == "" {
		return nil, errors.New("workerpool: executable is required")
	}
	cmd := exec.Command(spec.Executable, spec.Args...)
	cmd.Env = append(append([]string(nil), spec.Env...), "FAAS_PERSISTENT_WORKER=1")
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("workerpool: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("workerpool: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("workerpool: start handler: %w", err)
	}
	w := &worker{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout)}
	ready := make(chan error, 1)
	go func() {
		line, readErr := w.stdout.ReadBytes('\n')
		if readErr != nil {
			ready <- readErr
			return
		}
		var handshake struct {
			Ready bool `json:"__faas_ready"`
		}
		if err := json.Unmarshal(line, &handshake); err != nil {
			ready <- fmt.Errorf("decode ready handshake: %w", err)
			return
		}
		if !handshake.Ready {
			ready <- errors.New("missing ready handshake")
			return
		}
		ready <- nil
	}()
	select {
	case <-ctx.Done():
		w.close()
		return nil, ctx.Err()
	case err := <-ready:
		if err != nil {
			w.close()
			return nil, fmt.Errorf("workerpool: handler prewarm: %w", err)
		}
		return w, nil
	}
}

func (w *worker) close() {
	w.once.Do(func() {
		_ = w.stdin.Close()
		if w.cmd.Process != nil {
			_ = w.cmd.Process.Kill()
		}
		_ = w.cmd.Wait()
	})
}
