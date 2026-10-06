package main

// Issue #394 — queue introspection. Three read endpoints under the
// existing /v1/apps/{slug}/queues/* mount family:
//
//	GET /v1/apps/{slug}/queues/state         depth / in-flight / oldest
//	GET /v1/apps/{slug}/queues/peek          pending rows, no lease
//	GET /v1/apps/{slug}/queues/dead_letter   exhausted rows, no lease
//
// All three are read-only. None acquire a lease, none increment
// attempts, none mutate SQL state — see TestQueuePeek_ByteIdentical
// in handlers_queues_read_test.go for the property test.
//
// Error envelope: store-side failures route through ErrInternal, not
// ErrCapacity. ErrCapacity is reserved for admission-refusal scenarios
// (queue full, box at capacity); these read endpoints have no
// admission semantics, so a DB error would mislabel as "capacity" and
// misroute the on-call runbook.

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// queuePeekMaxLimit mirrors ListInvocationsForAccount's 200 cap. Past
// 200 rows the cursor pagination exists for a reason — callers should
// walk pages, not blast one giant response.
const queuePeekMaxLimit = 200

// queueState is the depth / in-flight / oldest-pending read endpoint.
// NO lease is acquired and no row is mutated. Free plans can still
// call this for diagnostics — the gate that locks Free out of queue
// features lives in queueSend/queueReceive, not here.
func (s *server) queueState(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !queueBindingProductionRequest(w, r) {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	stats, err := s.store.QueueState(r.Context(), app.ID)
	if err != nil {
		slog.Default().Error("queue state read failed", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrInternal("queue state"))
		return
	}
	now := time.Now().UTC()
	resp := api.QueueStateResponse{
		AppSlug:     app.Slug,
		Plan:        string(acct.Plan),
		PlanCap:     limits.MaxQueueDepth,
		Depth:       stats.Depth,
		InFlight:    stats.InFlight,
		GeneratedAt: now,
	}
	if !stats.OldestPendingAt.IsZero() {
		t := stats.OldestPendingAt
		age := int64(now.Sub(t).Seconds())
		if age < 0 {
			age = 0
		}
		resp.OldestPendingAt = &t
		resp.OldestPendingAgeSeconds = &age
	}
	writeJSON(w, http.StatusOK, resp)
}

// queuePeek returns up to `limit` pending rows (oldest first) WITHOUT
// acquiring a lease or incrementing attempts. Repeated calls return
// the same rows in the same order — the underlying SQL has no
// FOR UPDATE / FOR SHARE / advisory lock, so the row state is
// byte-identical across peeks. Cursor pagination matches the
// existing `?before=<id>` convention.
func (s *server) queuePeek(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !queueBindingProductionRequest(w, r) {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limit := 20
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= queuePeekMaxLimit {
			limit = n
		}
	}
	before := r.URL.Query().Get("before")
	rows, err := s.store.QueuePeek(r.Context(), app.ID, limit, before)
	if err != nil {
		slog.Default().Error("queue peek read failed", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrInternal("queue peek"))
		return
	}
	out := api.QueuePeekResponse{AppSlug: app.Slug, Messages: make([]api.QueuePeekMessage, 0, len(rows))}
	for _, inv := range rows {
		out.Messages = append(out.Messages, api.QueuePeekMessage{
			ID:        inv.ID,
			CreatedAt: inv.CreatedAt,
			Attempts:  inv.Attempts,
			Payload:   string(inv.Payload),
			LastError: inv.LastError,
		})
	}
	if len(rows) == limit && limit > 0 {
		out.NextBefore = rows[len(rows)-1].ID
	}
	writeJSON(w, http.StatusOK, out)
}

// queueDeadLetter lists messages that exhausted the plan's retry budget
// (state='dead_letter'). Ordered newest-first via the partial index
// added by migration 00060. Read-only: no lease, no mutation.
func (s *server) queueDeadLetter(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !queueBindingProductionRequest(w, r) {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limit := 20
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= queuePeekMaxLimit {
			limit = n
		}
	}
	before := r.URL.Query().Get("before")
	rows, err := s.store.QueueDeadLetter(r.Context(), app.ID, limit, before)
	if err != nil {
		slog.Default().Error("queue dead-letter read failed", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrInternal("queue dead_letter"))
		return
	}
	out := api.QueueDeadLetterResponse{AppSlug: app.Slug, Messages: make([]api.QueueDeadLetterMessage, 0, len(rows))}
	for _, inv := range rows {
		failedAt := time.Time{}
		if inv.CompletedAt != nil {
			failedAt = *inv.CompletedAt
		}
		out.Messages = append(out.Messages, api.QueueDeadLetterMessage{
			ID:        inv.ID,
			CreatedAt: inv.CreatedAt,
			FailedAt:  failedAt,
			Attempts:  inv.Attempts,
			LastError: inv.LastError,
			Payload:   string(inv.Payload),
		})
	}
	if len(rows) == limit && limit > 0 {
		out.NextBefore = rows[len(rows)-1].ID
	}
	writeJSON(w, http.StatusOK, out)
}
