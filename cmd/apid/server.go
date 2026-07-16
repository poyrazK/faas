package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// server is apid's HTTP service: the public REST API and the only writer to
// customer-intent tables (spec §4.2, §Component ownership). It validates plan
// quotas before any work, authenticates every request by API-key hash, and never
// talks to vmmd/builderd directly — it writes rows and lets owners react.
type server struct {
	store  state.Store
	log    *slog.Logger
	domain string // apps base domain for URLs
}

func newServer(store state.Store, log *slog.Logger, domain string) *server {
	if domain == "" {
		domain = "DOMAIN"
	}
	return &server{store: store, log: log, domain: domain}
}

// handler builds the route table (Go 1.22 method+wildcard patterns).
func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/account", s.auth(s.whoami))
	mux.HandleFunc("GET /v1/apps", s.auth(s.listApps))
	mux.HandleFunc("POST /v1/apps", s.auth(s.idempotent(s.createApp)))
	mux.HandleFunc("POST /v1/apps/{slug}/deployments", s.auth(s.idempotent(s.createDeployment)))
	return mux
}

// accountHandler is a handler that has already resolved the caller's account.
type accountHandler func(w http.ResponseWriter, r *http.Request, acct state.Account)

// auth authenticates by API-key hash and rejects inactive accounts (spec §11).
func (s *server) auth(next accountHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearerToken(r)
		if !api.ValidAPIKeyFormat(tok) {
			api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
				"Unauthorized", "provide a valid API key as a Bearer token"))
			return
		}
		acct, err := s.store.AccountByKeyHash(r.Context(), api.HashAPIKey(tok))
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
				"Unauthorized", "unknown API key"))
			return
		}
		if !acct.Active() {
			api.WriteProblem(w, api.NewProblem(http.StatusPaymentRequired, api.CodeBillingPastDue,
				"Account suspended", "resolve billing to continue: https://DOMAIN/billing"))
			return
		}
		next(w, r, acct)
	}
}

// idempotent replays a stored response for a repeated Idempotency-Key, or runs
// the handler and stores its response (spec §4.2: kept 24 h). Without the header
// it is a passthrough.
func (s *server) idempotent(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			next(w, r, acct)
			return
		}
		if status, body, err := s.store.GetIdempotent(r.Context(), acct.ID, key); err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(status)
			_, _ = w.Write(body)
			return
		}
		cap := &captureWriter{ResponseWriter: w, status: http.StatusOK}
		next(cap, r, acct)
		_ = s.store.PutIdempotent(r.Context(), acct.ID, key, cap.status, cap.body.Bytes())
	}
}

// captureWriter tees the response so idempotent() can persist it.
type captureWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *captureWriter) WriteHeader(status int) {
	c.status = status
	c.ResponseWriter.WriteHeader(status)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	c.body.Write(b)
	return c.ResponseWriter.Write(b)
}

// --- helpers ---------------------------------------------------------------

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}

// notFound writes a 404 problem, distinguishing missing rows.
func (s *server) notFound(w http.ResponseWriter, what string) {
	api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", what))
}

// ctx is a tiny helper to keep handler signatures clean.
func ctx(r *http.Request) context.Context { return r.Context() }
