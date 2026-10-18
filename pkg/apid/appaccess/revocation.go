package appaccess

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Polling is the backstop across every API/gateway process; it needs no
// in-process membership cache or reliable delivery of a NOTIFY event.
const orgAccessRecheckInterval = time.Second
const orgAccessRecheckTimeout = 2 * time.Second

func serveAuthorized(store Store, next authmw.AccountHandler, w http.ResponseWriter, r *http.Request, owner state.Account) {
	grant, shared := From(r)
	if !shared {
		next(w, r, owner)
		return
	}
	ServeFenced(store, next, w, r, owner, grant.Fence)
}

// ServeFenced applies the shared membership/credential fence and revocation
// response handling to an already-authorized resource request.
func ServeFenced(store state.OrgAccessStore, next authmw.AccountHandler, w http.ResponseWriter, r *http.Request, owner state.Account, fence state.OrgAccessFence) {
	ctx, cancel := state.WithOrgAccessFence(r.Context(), fence)
	defer cancel(nil)
	if memory, ok := store.(interface {
		WithMemoryIntentFence(context.Context) context.Context
	}); ok {
		ctx = memory.WithMemoryIntentFence(ctx)
	}
	r = r.WithContext(ctx)
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	if !read {
		w = &revocationWriter{ResponseWriter: w, ctx: ctx}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchAccess(ctx, store, func(cause error) {
			cancel(cause)
			controller := http.NewResponseController(w)
			if read {
				// Also release a stream stuck writing to a slow client.
				_ = controller.SetWriteDeadline(time.Now())
			} else {
				// Stop a slow upload while keeping the 403 response writable.
				_ = controller.SetReadDeadline(time.Now())
			}
		}, orgAccessRecheckInterval)
	}()
	defer func() { cancel(nil); <-done }()
	next(w, r, owner)
}

func watchAccess(ctx context.Context, store state.OrgAccessStore, cancel context.CancelCauseFunc, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkCtx, stop := context.WithTimeout(ctx, orgAccessRecheckTimeout)
			err := store.CheckOrgAccess(checkCtx)
			stop()
			if ctx.Err() != nil && !errors.Is(context.Cause(ctx), state.ErrOrgAccessRevoked) {
				return
			}
			if err != nil {
				cancel(state.ErrOrgAccessRevoked)
				return
			}
		}
	}
}

// Store errors are normally translated independently by each handler. A
// revoked grant must consistently return 403 rather than a generic 500/503.
// Streaming reads use cancellation instead because their headers are sent.
type revocationWriter struct {
	http.ResponseWriter
	ctx     context.Context
	written bool
	denied  bool
}

func (w *revocationWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *revocationWriter) WriteHeader(status int) {
	if w.written {
		return
	}
	w.written = true
	if errors.Is(context.Cause(w.ctx), state.ErrOrgAccessRevoked) {
		w.denied = true
		w.Header().Del("Content-Length")
		api.WriteProblem(w.ResponseWriter, api.ErrOrgRoleForbidden("continue a request after organization access changed"))
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *revocationWriter) Write(data []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	if w.denied {
		return len(data), nil
	}
	return w.ResponseWriter.Write(data)
}
