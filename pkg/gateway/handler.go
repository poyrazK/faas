package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// App is the routing target for a hostname.
type App struct {
	ID   string
	Plan api.Plan
}

// Backend is the seam between the edge and the rest of the platform (in
// production: the routing cache over Postgres, and schedd over gRPC). Splitting
// it out keeps the hot request path testable end-to-end without a real cluster.
type Backend interface {
	// Lookup resolves a hostname to its app (cache-first, spec §4.1).
	Lookup(ctx context.Context, host string) (App, bool)
	// Target returns a ready instance address (host:port) for the app, or false
	// when none is running and a wake is needed (the hot path returns true here).
	Target(appID string) (string, bool)
	// Wake ensures an instance is running via schedd admission + vmmd restore.
	Wake(ctx context.Context, appID string) error
}

// Handler is gatewayd's HTTP entrypoint: route → rate-limit → (wake-block if
// parked) → proxy (spec §4.1, §2). It is the only public listener on the box.
type Handler struct {
	backend Backend
	limiter *Limiter
	gate    *WakeGate
	// proxyFor builds the reverse proxy for an upstream address; overridable in
	// tests.
	proxyFor func(addr string) http.Handler
}

// NewHandler wires the edge with the spec's defaults (wake queue 512/30 s, spec
// §4.1). The host→app routing cache lives inside the Backend (it fronts Postgres).
func NewHandler(backend Backend) *Handler {
	h := &Handler{
		backend: backend,
		limiter: NewLimiter(),
		gate:    NewWakeGate(api.WakeQueueCap, time.Duration(api.WakeQueueTTLSeconds)*time.Second),
	}
	h.proxyFor = defaultProxy
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := hostname(r.Host)
	app, ok := h.backend.Lookup(r.Context(), host)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"No such app", fmt.Sprintf("no app is routed to %q", host)))
		return
	}

	// Per-app rate limit (spec §4.1). Over-limit → 429.
	if !h.limiter.Allow(app.ID, app.Plan) {
		w.Header().Set("Retry-After", "1")
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "rate_limited",
			"Rate limit exceeded", "slow down and retry"))
		return
	}

	// Cap request body either direction (spec §4.1).
	r.Body = http.MaxBytesReader(w, r.Body, api.MaxRequestBodyBytes)

	// Hot path: a ready instance already exists.
	addr, ready := h.backend.Target(app.ID)
	cold := false
	if !ready {
		if err := h.wake(r.Context(), app.ID); err != nil {
			writeWakeError(w, err)
			return
		}
		if addr, ready = h.backend.Target(app.ID); !ready {
			api.WriteProblem(w, api.ErrCapacity("woke but no instance became ready"))
			return
		}
		cold = true
	}

	if cold {
		// Cold-wake transparency (UX spec §6): let developers see the penalty.
		w.Header().Set("x-faas-wake", "cold")
	}
	h.proxyFor(addr).ServeHTTP(w, r)
}

// wake holds the request while schedd/vmmd bring an instance up, coalescing
// concurrent requests for the same app into one wake (spec §4.1).
func (h *Handler) wake(ctx context.Context, appID string) error {
	return h.gate.Wait(ctx, appID, func(ctx context.Context) error {
		return h.backend.Wake(ctx, appID)
	})
}

func writeWakeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrQueueFull):
		w.Header().Set("Retry-After", "5")
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"Briefly at capacity", "the wake queue is full; retry shortly"))
	default:
		var prob *api.Problem
		if errors.As(err, &prob) {
			api.WriteProblem(w, prob)
			return
		}
		api.WriteProblem(w, api.ErrCapacity("wake failed"))
	}
}

func defaultProxy(addr string) http.Handler {
	target := &url.URL{Scheme: "http", Host: addr}
	p := httputil.NewSingleHostReverseProxy(target)
	p.Transport = &http.Transport{
		ResponseHeaderTimeout: 60 * time.Second, // spec §4.1
	}
	return p
}

// hostname strips any port from the Host header and lowercases it.
func hostname(host string) string {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return strings.ToLower(host)
}
