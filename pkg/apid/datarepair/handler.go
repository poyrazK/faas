// Package datarepair exposes the internal application-bound repair API. It is
// mounted by an explicitly configured operator executor, never enabled globally.
package datarepair

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datarepair"
)

//go:embed web/*
var web embed.FS

type Principal struct {
	Scope      datarepair.Scope
	Actor      string
	CanApprove bool
	CanApply   bool
}

// Authenticate must derive scope and actor from trusted server configuration or
// validated identity. Supplying a scope in a request never grants authority.
type Authenticate func(*http.Request) (Principal, error)

type Handler struct {
	Engine       *datarepair.Engine
	Authenticate Authenticate
	Release      string
	// Coordinator permits the authenticated managed API to forward approval
	// and resolution actors. Keep false for standalone human operator workflows.
	Coordinator bool
	Exporter    *datarepair.SnapshotExporter
}

// Bearer binds a high-entropy operator token to one scope and actor. The token
// stays in server configuration; neither browser storage nor request bodies
// contain database credentials. Applications may supply their own authenticator.
func Bearer(token string, p Principal) Authenticate {
	return func(r *http.Request) (Principal, error) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(r.Header.Values("Authorization")) != 1 || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || len(token) < 32 || len(got) != len(token) || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 || p.Scope.Validate() != nil || p.Actor == "" {
			return Principal{}, errors.New("unauthorized")
		}
		return p, nil
	}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.asset)
	mux.HandleFunc("GET /app.js", h.asset)
	mux.HandleFunc("GET /app.css", h.asset)
	mux.HandleFunc("GET /sources.js", h.asset)
	mux.HandleFunc("GET /history.js", h.asset)
	mux.HandleFunc("GET /checks.js", h.asset)
	mux.HandleFunc("GET /validation.js", h.asset)
	mux.HandleFunc("GET /verification.js", h.asset)
	mux.HandleFunc("GET /replacement.js", h.asset)
	mux.HandleFunc("GET /resolution.js", h.asset)
	mux.HandleFunc("GET /v1/data-repair/sources", h.sources)
	mux.HandleFunc("GET /v1/data-repair/doctor", h.doctor)
	mux.HandleFunc("GET /v1/data-repair/sources/{id}/history", h.history)
	mux.HandleFunc("POST /v1/data-repair/plans", h.create)
	mux.HandleFunc("GET /v1/data-repair/plans/{id}", h.get)
	mux.HandleFunc("POST /v1/data-repair/plans/{id}/validate", h.validate)
	mux.HandleFunc("POST /v1/data-repair/plans/{id}/verify", h.verify)
	mux.HandleFunc("POST /v1/data-repair/plans/{id}/replace", h.replace)
	mux.HandleFunc("POST /v1/data-repair/plans/{id}/resolve", h.resolve)
	mux.HandleFunc("POST /v1/data-repair/plans/{id}/snapshot", h.snapshot)
	mux.HandleFunc("POST /v1/data-repair/plans/{id}/rehearsal-evidence", h.recordRehearsal)
	mux.HandleFunc("POST /v1/data-repair/plans/{id}/{action}", h.action)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		mux.ServeHTTP(w, r)
	})
}

func (h *Handler) asset(w http.ResponseWriter, r *http.Request) {
	name, kind, ok := standaloneRepairAsset(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", kind)
	b, err := web.ReadFile(name)
	if err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write(b)
}

func standaloneRepairAsset(path string) (string, string, bool) {
	switch path {
	case "/":
		return "web/index.html", "text/html; charset=utf-8", true
	case "/app.css":
		return "web/app.css", "text/css; charset=utf-8", true
	case "/app.js", "/sources.js", "/doctor.js", "/history.js", "/checks.js", "/validation.js", "/verification.js", "/replacement.js", "/resolution.js":
		return "web" + path, "text/javascript; charset=utf-8", true
	default:
		return "", "", false
	}
}

func (h *Handler) principal(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	if h.Authenticate == nil || h.Engine == nil {
		problem(w, datarepair.ErrUnavailable)
		return Principal{}, false
	}
	p, err := h.Authenticate(r)
	if err != nil || p.Scope.Validate() != nil || p.Actor == "" {
		api.WriteProblem(w, api.NewProblem(401, "repair_unauthorized", "Authentication required", "Use an authorized repair executor identity"))
		return Principal{}, false
	}
	return p, true
}

type request struct {
	Actor             string `json:"actor,omitempty"`
	PlanID            string `json:"plan_id,omitempty"`
	OperationID       string `json:"operation_id,omitempty"`
	Digest            string `json:"digest,omitempty"`
	RepairOperationID string `json:"repair_operation_id,omitempty"`
}

func decode(w http.ResponseWriter, r *http.Request) (request, bool) {
	var in request
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(api.DataRepairMaxRequestBytes)))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil {
		problem(w, datarepair.ErrInvalid)
		return in, false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		problem(w, datarepair.ErrInvalid)
		return in, false
	}
	return in, true
}

func write(w http.ResponseWriter, p datarepair.Plan, err error) {
	if err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p)
}

func problem(w http.ResponseWriter, err error) {
	status, code, detail := 503, "repair_unavailable", "The database or isolated rehearsal runner is unavailable"
	var stage *datarepair.RehearsalError
	var failed *datarepair.CheckError
	if errors.As(err, &stage) {
		detail = stage.Error()
	}
	switch {
	case errors.As(err, &failed):
		status, code, detail = 409, "repair_check_failed", "A declared business check failed; reload the plan for individual results"
	case errors.Is(err, datarepair.ErrInvalid):
		status, code, detail = 400, "repair_invalid", "Provide a valid bounded repair request"
	case errors.Is(err, datarepair.ErrNotFound):
		status, code, detail = 404, "repair_not_found", "The operation or plan is unavailable in this scope"
	case errors.Is(err, datarepair.ErrConflict):
		status, code, detail = 409, "repair_conflict", "The approved state changed; inspect and review a new plan"
	case errors.Is(err, datarepair.ErrPurged):
		status, code, detail = http.StatusGone, "repair_payload_purged", "Captured values have been purged under the retention policy; retained identities and receipts remain available"
	case errors.Is(err, datarepair.ErrOutcomeUnknown):
		code, detail = "repair_outcome_unknown", err.Error()
	}
	api.WriteProblem(w, api.NewProblem(status, code, "Selective repair", detail))
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	in, ok := decode(w, r)
	if !ok {
		return
	}
	if in.Digest != "" || in.RepairOperationID != "" || in.Actor != "" {
		problem(w, datarepair.ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	out, err := h.Engine.Prepare(ctx, p.Scope, in.OperationID, in.PlanID)
	write(w, out, err)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	out, err := h.Engine.Get(r.Context(), p.Scope, r.PathValue("id"))
	write(w, out, err)
}

func (h *Handler) action(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}
	in, ok := decode(w, r)
	if !ok {
		return
	}
	if in.OperationID != "" || in.PlanID != "" || (in.Actor != "" && (!h.Coordinator || r.PathValue("action") != "approve")) {
		problem(w, datarepair.ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	var out datarepair.Plan
	var err error
	switch r.PathValue("action") {
	case "rehearse":
		if in.RepairOperationID != "" {
			err = datarepair.ErrInvalid
		} else {
			out, err = h.Engine.Rehearse(ctx, p.Scope, r.PathValue("id"), in.Digest)
		}
	case "approve":
		if !p.CanApprove {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "repair_approval_forbidden", "Approval forbidden", "This identity cannot approve repair plans"))
			return
		}
		if in.RepairOperationID != "" {
			err = datarepair.ErrInvalid
		} else {
			actor := p.Actor
			if h.Coordinator && in.Actor != "" {
				actor = in.Actor
			}
			out, err = h.Engine.Approve(ctx, p.Scope, r.PathValue("id"), in.Digest, actor)
		}
	case "apply":
		if !p.CanApply {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "repair_apply_forbidden", "Apply forbidden", "This identity cannot apply repair plans"))
			return
		}
		out, err = h.Engine.Apply(ctx, p.Scope, r.PathValue("id"), in.Digest, in.RepairOperationID, h.Release)
	default:
		err = datarepair.ErrNotFound
	}
	write(w, out, err)
}
