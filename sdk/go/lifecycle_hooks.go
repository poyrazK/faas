package faas

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// Default paths of the gregale.yaml lifecycle hooks (docs/cold-wake.md). Set
// LifecycleHooks.BeforeCheckpointPath/AfterRestorePath when gregale.yaml
// names others.
const (
	DefaultBeforeCheckpointPath = "/internal/before-checkpoint"
	DefaultAfterRestorePath     = "/internal/after-restore"
	// DefaultLifecycleHookBudget leaves headroom under the platform's 2 s
	// hook ceiling, so a slow callback fails the hook instead of the wake.
	DefaultLifecycleHookBudget = 1500 * time.Millisecond
)

// LifecycleHooks serves Gregale's before_checkpoint and after_restore
// callbacks (ADR-20261106105015 §2). It answers only loopback POSTs that carry
// the platform's marker header, runs the registered callbacks in order under
// one deadline, and never runs a kind's callbacks twice at once: a delivery
// that arrives while a run is still going, even one whose delivery timed out,
// waits for that run. Callbacks still run again on a later capture or restore.
//
// Mount it in front of the app's handler:
//
//	hooks := faas.NewLifecycleHooks()
//	hooks.RecycleIdleConnections(db, 10) // a *sql.DB
//	http.ListenAndServe(":"+os.Getenv("PORT"), hooks.Middleware(mux))
//
// and declare the hooks in gregale.yaml under lifecycle.before_checkpoint and
// lifecycle.after_restore. Secrets keep their own SIGHUP path through
// SecretReloadCoordinator; do not reload them here.
type LifecycleHooks struct {
	BeforeCheckpointPath string
	AfterRestorePath     string
	Budget               time.Duration

	mu       sync.Mutex
	before   []func(context.Context) error
	after    []func(context.Context) error
	inflight map[string]*lifecycleRun
}

type lifecycleRun struct {
	done chan struct{}
	err  error
}

// NewLifecycleHooks returns hooks on the default paths and budget.
func NewLifecycleHooks() *LifecycleHooks {
	return &LifecycleHooks{BeforeCheckpointPath: DefaultBeforeCheckpointPath, AfterRestorePath: DefaultAfterRestorePath, Budget: DefaultLifecycleHookBudget}
}

// OnBeforeCheckpoint registers work to do before Gregale captures a new init
// snapshot: flush buffers, close connections. A returned error refuses the
// capture.
func (h *LifecycleHooks) OnBeforeCheckpoint(fn func(context.Context) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.before = append(h.before, fn)
}

// OnAfterRestore registers work to do when an instance restores from a
// snapshot or resumes frozen: drop stale connections, refresh values that
// must be unique per instance. A returned error discards the instance and the
// request falls back to a fresh boot. Keep it short: it adds to wake latency.
func (h *LifecycleHooks) OnAfterRestore(fn func(context.Context) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.after = append(h.after, fn)
}

// IdleConnectionPool is the part of *sql.DB RecycleIdleConnections uses.
type IdleConnectionPool interface {
	SetMaxIdleConns(n int)
}

// RecycleIdleConnections closes pool's idle connections before a checkpoint
// and after a restore, then allows maxIdle again. Their sockets point at a
// connection the database closed while the instance was parked or frozen;
// closing them is local and fast, and the pool dials fresh ones on demand.
func (h *LifecycleHooks) RecycleIdleConnections(pool IdleConnectionPool, maxIdle int) {
	recycle := func(context.Context) error {
		pool.SetMaxIdleConns(0)
		pool.SetMaxIdleConns(maxIdle)
		return nil
	}
	h.OnBeforeCheckpoint(recycle)
	h.OnAfterRestore(recycle)
}

// Middleware answers the hook paths and passes every other request to next.
func (h *LifecycleHooks) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.serve(w, r) {
			next.ServeHTTP(w, r)
		}
	})
}

// ServeHTTP answers the hook paths and 404s anything else, for mounting on
// those two paths of a mux.
func (h *LifecycleHooks) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.serve(w, r) {
		http.NotFound(w, r)
	}
}

func (h *LifecycleHooks) serve(w http.ResponseWriter, r *http.Request) bool {
	beforePath, afterPath := h.BeforeCheckpointPath, h.AfterRestorePath
	if beforePath == "" {
		beforePath = DefaultBeforeCheckpointPath
	}
	if afterPath == "" {
		afterPath = DefaultAfterRestorePath
	}
	var kind, header string
	switch r.URL.Path {
	case beforePath:
		kind, header = "before_checkpoint", "X-Faas-Before-Checkpoint"
	case afterPath:
		kind, header = "after_restore", "X-Faas-After-Restore"
	default:
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	// Gregale strips inbound x-faas-* headers at the public proxy; the
	// loopback check also refuses a caller inside the app's network.
	if r.Header.Get(header) != "1" || !loopbackPeer(r.RemoteAddr) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return true
	}
	if err := h.run(r.Context(), kind); err != nil {
		http.Error(w, "lifecycle hook failed", http.StatusInternalServerError)
		return true
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}

// run waits for one kind's callbacks, at most one budget. A run stays
// registered until its callbacks return, even after a delivery stops waiting
// for it, so a retried delivery waits for that same run instead of starting
// the callbacks again beside it.
func (h *LifecycleHooks) run(ctx context.Context, kind string) error {
	h.mu.Lock()
	if h.inflight == nil {
		h.inflight = map[string]*lifecycleRun{}
	}
	budget := h.Budget
	if budget <= 0 {
		budget = DefaultLifecycleHookBudget
	}
	current := h.inflight[kind]
	if current == nil {
		callbacks := h.after
		if kind == "before_checkpoint" {
			callbacks = h.before
		}
		current = &lifecycleRun{done: make(chan struct{})}
		h.inflight[kind] = current
		go h.execute(kind, current, append([]func(context.Context) error(nil), callbacks...), budget)
	}
	h.mu.Unlock()

	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-current.done:
		return current.err
	case <-timer.C:
		return errors.New("faas: lifecycle callbacks exceeded their budget")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// execute runs callbacks in order until one fails or the budget's context
// ends; it returns only when the running callback returns.
func (h *LifecycleHooks) execute(kind string, current *lifecycleRun, callbacks []func(context.Context) error, budget time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	for _, callback := range callbacks {
		if ctx.Err() != nil {
			current.err = errors.New("faas: lifecycle callbacks exceeded their budget")
			break
		}
		if err := callLifecycle(ctx, callback); err != nil {
			current.err = err
			break
		}
	}
	h.mu.Lock()
	delete(h.inflight, kind)
	h.mu.Unlock()
	close(current.done)
}

func callLifecycle(ctx context.Context, callback func(context.Context) error) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("faas: lifecycle callback panicked")
		}
	}()
	return callback(ctx)
}

func loopbackPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
