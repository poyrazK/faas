// Package local runs a function's handler through the production runner core
// outside a microVM, for `gregale dev --local` (ADR-20261107210000). It is
// the only door from the CLI into guest/runners/internal: the same request
// envelope, worker pool, streaming and failure handling as a deployed
// function, so local behaviour follows production when the core changes.
package local

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/guest/runners/internal"
	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
)

// Function is one handler process: what the runner would exec in the guest.
type Function struct {
	// Runtime labels log lines, such as "node24" or "go124".
	Runtime string
	// Executable and Args start the handler: node or python3 with the
	// generated adapter, or a compiled Go handler.
	Executable string
	Args       []string
	// Env is the handler's whole environment.
	Env []string
	// HandlerPath is the file carrying the persistent-protocol marker: the
	// adapter, or the Go binary.
	HandlerPath string
	// Compiled marks a binary, whose protocol marker sits anywhere in the
	// file rather than in its first line.
	Compiled bool
}

func (f Function) spec() workerpool.Spec {
	return workerpool.Spec{
		Executable: f.Executable, Args: f.Args, Env: f.Env,
		HandlerPath: f.HandlerPath, MarkerAnywhere: f.Compiled,
	}
}

// Handler serves HTTP requests through the function, as the runner's "/"
// route does in the guest.
func Handler(f Function) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		internal.Serve(w, r, internal.ServeConfig{Runtime: f.Runtime, Spec: f.spec()})
	})
}

// Reload replaces the function's workers so the next request loads new
// code, and reports how many idle workers it closed. Busy workers finish
// their request first.
func Reload(f Function) int { return workerpool.RecycleSpec(f.spec()) }

// Prewarm starts one worker and loads the handler module, as the runner does
// before it reports ready, so a broken handler fails at start.
func Prewarm(ctx context.Context, f Function) error {
	_, err := workerpool.PrewarmIfSupported(ctx, f.spec())
	return err
}

// HandlerTimeout is the least time one invocation gets.
const HandlerTimeout = internal.HandlerTimeout
