// Package workerpool keeps generated function adapters alive between
// invocations. Legacy protocol handlers remain one-process-per-request and are
// selected automatically when the adapter marker is absent. A worker serves
// one request at a time unless its ready handshake advertises more; such a
// shared worker multiplexes requests by id (mux.go, ADR-1342).
package workerpool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

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
	// MarkerAnywhere scans the whole handler file for the protocol marker.
	// Compiled handlers (Go) carry it in their data section rather than on a
	// leading comment line, so the 4 KiB source prefix check cannot see it.
	MarkerAnywhere bool
	// RequireResourceLifecycle is trusted runner wiring. An old adapter must
	// not acknowledge checkpoint without owning registered resources.
	RequireResourceLifecycle bool
}

var (
	supportCache sync.Map // handler path -> bool
	pools        sync.Map // command identity -> *pool
)

// InvokeIfSupported invokes a marked, newline-framed adapter through a reused
// worker. handled=false asks the caller to retain the legacy one-shot path.
func InvokeIfSupported(ctx context.Context, spec Spec, request, response any) (handled bool, err error) {
	if !supportsPersistentProtocol(spec) {
		if spec.RequireResourceLifecycle {
			return true, errors.New("workerpool: generated resource lifecycle adapter required")
		}
		return false, nil
	}
	return true, poolFor(spec).invoke(ctx, request, response)
}

// OpenIfSupported sends request to a reused worker and returns the worker's
// response lines: one complete response, or a streamed response's head and
// the frames after it (ADR-999). The caller reads what it needs and then
// closes the Lines, healthy only when the response ended cleanly (and, when
// negotiated, execution completed). A worker left mid-response would hand its
// remaining frames to the next request. handled=false retains the one-shot path.
func OpenIfSupported(ctx context.Context, spec Spec, request any) (lines *Lines, handled bool, err error) {
	if !supportsPersistentProtocol(spec) {
		if spec.RequireResourceLifecycle {
			return nil, true, errors.New("workerpool: generated resource lifecycle adapter required")
		}
		return nil, false, nil
	}
	lines, err = poolFor(spec).open(ctx, request)
	return lines, true, err
}

// Lines is one invocation's response stream from a pooled worker.
type Lines struct {
	ctx  context.Context
	pool *pool
	w    *worker
	// s and id name the request on a shared worker; s is nil on an
	// exclusive one, whose stdout carries only this request.
	s          *stream
	id         string
	once       sync.Once
	released   chan struct{}
	completion bool
	completed  atomic.Bool
}

// Next returns the worker's next stdout line, or ctx's error once the
// invocation is canceled or out of time.
func (l *Lines) Next() ([]byte, error) {
	return l.NextContext(l.ctx)
}

// NextContext reads with a new lifetime after a completed HTTP response. Calls
// must remain sequential; only the invocation's owner may read its frames.
func (l *Lines) NextContext(ctx context.Context) ([]byte, error) {
	line, err := l.next(ctx)
	if l.completion && err == nil {
		var frame struct {
			Frame string `json:"frame"`
		}
		if json.Unmarshal(line, &frame) == nil && frame.Frame == "complete" {
			l.completed.Store(true)
		}
	}
	return line, err
}

func (l *Lines) next(ctx context.Context) ([]byte, error) {
	if l.s != nil {
		return l.s.next(ctx)
	}
	type readResult struct {
		line []byte
		err  error
	}
	result := make(chan readResult, 1)
	go func() {
		line, readErr := l.w.stdout.ReadBytes('\n')
		result <- readResult{line: line, err: readErr}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case got := <-result:
		return got.line, got.err
	}
}

// Released closes after execution has finished, cancellation cleanup has been
// acknowledged, or the worker has been retired. A response alone is not enough.
func (l *Lines) Released() <-chan struct{} { return l.released }

// Close returns the worker to its pool, or retires it when healthy is false.
// A shared worker keeps serving its other requests: an unhealthy close
// cancels only this one.
func (l *Lines) Close(healthy bool) {
	l.once.Do(func() {
		healthy = healthy && (!l.completion || l.completed.Load())
		if l.s == nil {
			l.pool.release(l.w, healthy)
			close(l.released)
			return
		}
		l.w.mux.unregister(l.id)
		if !healthy {
			// Cancellation is not completion. Retain the slot until the adapter
			// has finished cleanup or the worker has been retired.
			grace := cancelAckGrace
			go func() {
				defer close(l.released)
				defer l.pool.release(l.w, true)
				l.pool.cancel(l.ctx, l.w, l.id, grace)
			}()
			return
		}
		l.pool.release(l.w, true)
		close(l.released)
	})
}

// PrewarmIfSupported starts and fully initializes one generated adapter before
// the runner advertises readiness. The idle process is then included in the
// init snapshot, so a restored VM does not pay interpreter and module-import
// startup on its first request.
func PrewarmIfSupported(ctx context.Context, spec Spec) (handled bool, err error) {
	if !supportsPersistentProtocol(spec) {
		if spec.RequireResourceLifecycle {
			return true, errors.New("workerpool: generated resource lifecycle adapter required")
		}
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
	key := fmt.Sprintf("%t\x00%s", spec.RequireResourceLifecycle, strings.Join(append([]string{spec.Executable}, spec.Args...), "\x00"))
	value, _ := pools.LoadOrStore(key, newPool(spec))
	return value.(*pool)
}

func supportsPersistentProtocol(spec Spec) bool {
	path := spec.HandlerPath
	if cached, ok := supportCache.Load(path); ok {
		return cached.(bool)
	}
	f, err := os.Open(path) //nolint:forbidigo // HandlerPath is the runner-owned handler path; this reads only its protocol marker.
	if err != nil {
		supportCache.Store(path, false)
		return false
	}
	defer func() { _ = f.Close() }()
	var supported bool
	if spec.MarkerAnywhere {
		supported, err = containsMarker(f)
		supported = err == nil && supported
	} else {
		prefix, readErr := io.ReadAll(io.LimitReader(f, 4096))
		supported = readErr == nil && strings.Contains(string(prefix), protocolMarker)
	}
	supportCache.Store(path, supported)
	return supported
}

// containsMarker streams r looking for protocolMarker, carrying a marker-sized
// overlap between chunks so a match split across a read boundary is found.
func containsMarker(r io.Reader) (bool, error) {
	marker := []byte(protocolMarker)
	buf := make([]byte, 64*1024)
	carry := 0
	for {
		n, err := r.Read(buf[carry:])
		window := buf[:carry+n]
		if bytes.Contains(window, marker) {
			return true, nil
		}
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		carry = min(len(marker)-1, len(window))
		copy(buf, window[len(window)-carry:])
	}
}

type pool struct {
	spec Spec
	mu   sync.Mutex
	idle []*worker
	// shared holds the live workers that serve several requests at once.
	shared []*worker
	live   int
	// starting counts workers being started; slots is the concurrency the
	// latest worker advertised. While a shared worker is starting, others
	// wait for its slots instead of starting more interpreters.
	starting int
	slots    int
	changed  chan struct{}
	// generation advances on Recycle; a worker from an older generation is
	// closed instead of being reused.
	generation   uint64
	broadcasting bool
}

func newPool(spec Spec) *pool { return &pool{spec: spec, changed: make(chan struct{})} }

// Recycle closes every worker started before the call, in every pool, and
// returns how many it closed. Later invocations start fresh workers. A
// snapshot restore calls it before the instance serves traffic: a restored
// worker would otherwise resume with the in-process random state it held at
// capture, shared with every sibling restored from the same snapshot
// (GHSA-24j2-p895-mwc9, ADR-687). Busy workers are closed when released.
func Recycle() int {
	closed := 0
	pools.Range(func(_, value any) bool {
		closed += value.(*pool).recycle()
		return true
	})
	return closed
}

func (p *pool) recycle() int {
	p.mu.Lock()
	p.generation++
	stale := p.idle
	p.idle = nil
	p.live -= len(stale)
	for _, w := range append([]*worker(nil), p.shared...) {
		if w.active == 0 && p.dropSharedLocked(w) {
			stale = append(stale, w)
		}
	}
	p.notifyLocked()
	p.mu.Unlock()
	for _, w := range stale {
		w.close()
	}
	return len(stale)
}

func (p *pool) acquire(ctx context.Context) (*worker, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p.mu.Lock()
		if p.broadcasting {
			changed := p.changed
			p.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-changed:
				continue
			}
		}
		if w := p.sharedWithRoomLocked(); w != nil {
			w.active++
			p.mu.Unlock()
			return w, nil
		}
		if n := len(p.idle); n > 0 {
			w := p.idle[n-1]
			p.idle = p.idle[:n-1]
			p.mu.Unlock()
			return w, nil
		}
		if p.live < maxWorkers && (p.slots <= 1 || p.starting == 0) {
			p.live++ // Reserve before starting: concurrent misses cannot oversubscribe.
			p.starting++
			generation := p.generation
			p.mu.Unlock()
			return p.start(ctx, generation)
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

// start runs a new worker in a slot acquire reserved. A shared worker joins
// the pool with this request already holding one of its slots.
func (p *pool) start(ctx context.Context, generation uint64) (*worker, error) {
	w, err := startWorker(ctx, p.spec)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.starting--
	defer p.notifyLocked()
	if err != nil {
		p.live--
		return nil, err
	}
	w.generation = generation
	p.slots = w.slots
	if w.slots > 1 || w.completion {
		w.mux = newMux()
		w.active = 1
		p.shared = append(p.shared, w)
		go p.demux(w)
	}
	return w, nil
}

// sharedWithRoomLocked returns the least busy current shared worker with a
// free slot.
func (p *pool) sharedWithRoomLocked() *worker {
	var best *worker
	for _, w := range p.shared {
		if w.gone || w.generation != p.generation || w.active >= w.slots {
			continue
		}
		if best == nil || w.active < best.active {
			best = w
		}
	}
	return best
}

// dropSharedLocked removes a shared worker from the pool, once.
func (p *pool) dropSharedLocked(w *worker) bool {
	if w.removed {
		return false
	}
	w.removed = true
	p.live--
	for i, candidate := range p.shared {
		if candidate == w {
			p.shared = append(p.shared[:i], p.shared[i+1:]...)
			break
		}
	}
	return true
}

// retire removes a shared worker that exited or stopped responding and
// kills it. Its in-flight requests fail with the worker.
func (p *pool) retire(w *worker) {
	p.mu.Lock()
	w.gone = true
	p.dropSharedLocked(w)
	p.notifyLocked()
	p.mu.Unlock()
	w.close()
}

func (p *pool) notifyLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
}

func (p *pool) release(w *worker, healthy bool) {
	if w.mux != nil {
		p.releaseSlot(w)
		return
	}
	p.mu.Lock()
	reuse := healthy && w.generation == p.generation
	if reuse {
		p.idle = append(p.idle, w)
	} else {
		p.live--
	}
	p.notifyLocked()
	p.mu.Unlock()
	if !reuse {
		w.close()
	}
}

// releaseSlot returns one request's slot on a shared worker. A worker from
// before a Recycle is closed when its last request leaves.
func (p *pool) releaseSlot(w *worker) {
	p.mu.Lock()
	w.active--
	stale := w.active == 0 && w.generation != p.generation && p.dropSharedLocked(w)
	p.notifyLocked()
	p.mu.Unlock()
	if stale {
		w.close()
	}
}

// invoke sends one request and decodes a single-line response.
func (p *pool) invoke(ctx context.Context, request, response any) error {
	lines, err := p.open(ctx, request)
	if err != nil {
		return err
	}
	line, err := lines.Next()
	if err != nil {
		lines.Close(false)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("workerpool: read response: %w", err)
	}
	if err := json.Unmarshal(line, response); err != nil {
		lines.Close(false)
		return fmt.Errorf("workerpool: decode response: %w", err)
	}
	lines.Close(true)
	return nil
}

func (p *pool) open(ctx context.Context, request any) (*Lines, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("workerpool: encode request: %w", err)
	}
	w, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	completion := false
	if request, ok := request.(interface{ RequiresCompletion() bool }); ok {
		completion = request.RequiresCompletion()
	}
	if completion && !w.completion {
		p.release(w, true)
		return nil, errors.New("workerpool: adapter does not support execution completion")
	}
	if w.mux != nil {
		lines, err := p.openShared(ctx, w, payload)
		if lines != nil {
			lines.completion = completion
		}
		return lines, err
	}
	payload = append(payload, '\n')
	if _, err := w.stdin.Write(payload); err != nil {
		p.release(w, false)
		return nil, fmt.Errorf("workerpool: write request: %w", err)
	}
	return &Lines{ctx: ctx, pool: p, w: w, released: make(chan struct{})}, nil
}

func (p *pool) openShared(ctx context.Context, w *worker, payload []byte) (*Lines, error) {
	id, s := w.mux.register()
	line, err := withID(payload, id)
	if err == nil {
		var broken bool
		if broken, err = w.send(ctx, line); broken {
			p.retire(w)
		}
	}
	if err != nil {
		w.mux.unregister(id)
		p.releaseSlot(w)
		return nil, err
	}
	return &Lines{ctx: ctx, pool: p, w: w, s: s, id: id, released: make(chan struct{})}, nil
}

type worker struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     *bufio.Reader
	once       sync.Once
	generation uint64
	// slots is how many requests the worker serves at once, from its ready
	// handshake; 1 is exclusive unless completion uses request IDs.
	slots int
	// completion version 1 separates a response from execution completion and
	// uses request IDs even on a one-slot worker.
	completion bool
	// mux is set when slots > 1 or execution completion uses request IDs.
	// The pool's mutex guards active, gone and removed.
	mux     *mux
	active  int
	gone    bool
	removed bool
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
			Ready                    bool `json:"__faas_ready"`
			Concurrency              int  `json:"concurrency"`
			CompletionVersion        int  `json:"completion_version"`
			ResourceLifecycleVersion int  `json:"resource_lifecycle_version"`
		}
		if err := json.Unmarshal(line, &handshake); err != nil {
			ready <- fmt.Errorf("decode ready handshake: %w", err)
			return
		}
		if !handshake.Ready {
			ready <- errors.New("missing ready handshake")
			return
		}
		if spec.RequireResourceLifecycle && (handshake.CompletionVersion != 1 || handshake.ResourceLifecycleVersion != 1) {
			ready <- errors.New("adapter does not support resource lifecycle; rebuild required")
			return
		}
		w.slots = min(max(handshake.Concurrency, 1), maxWorkerSlots)
		w.completion = handshake.CompletionVersion == 1
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
