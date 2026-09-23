// SSE live-update endpoint (M7.5 slice 6, ADR-011 dashboard).
//
// `GET /v1/events` opens a Server-Sent Event stream of pg_notify
// frames relevant to the calling account. Production wires Postgres
// LISTEN/NOTIFY; tests inject a recording stub so the suite doesn't
// need a live DB.
//
// Auth: session cookie OR API key (Bearer). We accept both because
// the dashboard HTML pages call /v1/events from the browser (cookie)
// and the CLI's --watch flag would call it from a curl (Bearer).
//
// Filtering rule (spec §6.1 + ADR-006): the apid writer that emitted
// the payload tags it with an `app_id` or `account_id`; the SSE
// handler caches the caller's owned app IDs in-memory at request
// time and drops any frame whose `app_id` doesn't belong here or
// whose `account_id` doesn't match.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apislogs"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// eventsChannels is the set we subscribe to. Keeping it flat makes the
// account-scoped stream easy to audit;
// Move 3 widens it to include NotifyInvocationDone so the dashboard
// reacts when a customer runs `faas invoke` from another terminal.
// Mirrored in cmd/apid/sse_fanin.go::sseChannels for the in-process
// publisher; the two lists must stay in lock-step because the
// fan-in's job is to be a faithful mirror of pg_notify for the
// in-process broadcaster.
//
// Idempotency note (Move 3): invocation_done is published by BOTH the
// DB trigger (migrations/00031_invocations_notify.sql:36-53) AND the
// schedd drain's emitDone (pkg/sched/drain.go). Consumers MUST dedup
// on (invocation_id, state); the first frame wins. The dashboard's
// htmx-sse consumer is naturally idempotent (fragment re-render) and
// the CLI's faas tail printer prints the same id+state line twice
// without harm.
var eventsChannels = []string{
	db.NotifyAppChanged,
	db.NotifyDeploymentChanged,
	db.NotifyInstanceChanged,
	db.NotifyCronFired,
	db.NotifyQuotaWarning,
	db.NotifyBillingPastDue,
	db.NotifyInvocationDone,
	db.NotifyDebugRegressionChanged,
	// Wave 0 PR-C / ADR-047: stateless-advisory frame from
	// cmd/apid/advisory_receiver.go::ForwardStatelessAdvisory.
	// Payload is the small summary (app_id, instance, n, sample_path);
	// the audit row at /v1/audit-events?kind_prefix=stateless.advisory
	// is the detail surface. normalizedEventsFrameForAccount below enforces
	// account-scoping; consumers can subscribe via `faas tail
	// --include-stateless` or the dashboard's advisory tab.
	db.NotifyStatelessAdvisory,
}

// eventsHandler is the SSE handler. It accepts either a session cookie
// (dashboard) or an API key (CLI), resolves the account, then dumps
// every relevant pg_notify frame to the client as `event: <kind>`
// frames until the client disconnects.
//
// API-key callers must hold at least the "apps:read" scope (the read
// surface, see ScopesReadSurface); session-cookie callers are
// implicitly admin. IAM-1, ADR-034 rev2.
func (s *server) eventsHandler(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Move 3 / §12: apid_sse_clients tracks the number of open
		// /v1/events connections. Increment before auth (so an
		// unauthenticated 401 still reflects a connection attempt)
		// and defer Dec on every exit path. nil-safe — production
		// always wires OpsMetrics, but unit tests don't.
		if s.ops != nil {
			s.ops.SSEClients().Inc()
			defer s.ops.SSEClients().Dec()
		}
		acct, key, ok := resolveEventsCaller(r, s)
		if !ok {
			api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
				"Unauthorized", "session cookie or API key required"))
			return
		}
		if key != nil && !principalHasScope(principal{Acct: acct, Key: key}, api.ScopesReadSurface) {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
				"Insufficient scope", "event stream requires the apps:read or admin scope"))
			return
		}
		ownedApps := s.buildOwnedAppCache(r.Context(), acct.ID)

		apislogs.StartSSE(w)
		flusher, _ := w.(http.Flusher)

		ch, cancel, err := s.notif.Subscribe(r.Context(), eventsChannels)
		if err != nil {
			s.log.ErrorContext(r.Context(), "subscribe event stream", "account_id", acct.ID, "err", err)
			payload, _ := json.Marshal(struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}{api.CodeEventStreamUnavailable, "The event stream is temporarily unavailable. Reconnect in a moment."})
			_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", payload)
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		defer cancel()

		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()

		// Snapshot the cache once per connection — apps don't change
		// mid-stream for slice 6 (no listener-side refresh; reconnect
		// to pick up new apps).
		for {
			select {
			case <-r.Context().Done():
				return
			case n, ok := <-ch:
				if !ok {
					return
				}
				scoped, ok := normalizedEventsFrameForAccount(n, acct.ID, ownedApps)
				if !ok {
					if n.Channel == db.NotifyAppChanged {
						if _, err := db.ParseAppChangedPayload(n.Payload); err != nil {
							s.ops.ObserveNotificationPayloadRejected(db.NotifyAppChanged, "sse")
						}
					}
					continue
				}
				writeSSEFrame(w, scoped)
				if flusher != nil {
					flusher.Flush()
				}
			case <-heartbeat.C:
				_, _ = fmt.Fprint(w, ":\n\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}
}

// resolveEventsCaller accepts either a session cookie (dashboard) or
// an API key (CLI), pulling the matching account + key off the request.
// The key is nil when the caller authenticated via session cookie (in
// which case the caller is implicitly admin). Returns the account,
// key, and true; or false if neither auth path matched.
func resolveEventsCaller(r *http.Request, s *server) (state.Account, *state.APIKey, bool) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if env, err := s.sessions.Verify(c.Value); err == nil {
			if acct, err := s.store.AccountByID(r.Context(), env.AccountID); err == nil && acct.Active() {
				return acct, nil, true
			}
		}
	}
	if h := r.Header.Get("Authorization"); len(h) > 7 && h[:7] == "Bearer " {
		tok := h[7:]
		if api.ValidAPIKeyFormat(tok) {
			if acct, key, err := s.store.AuthenticateKey(r.Context(), api.HashAPIKey(tok)); err == nil && acct.Active() {
				return acct, &key, true
			}
		}
	}
	return state.Account{}, nil, false
}

// buildOwnedAppCache returns a lookup set of app IDs that belong to
// the account. O(apps) at request time; the SSE consumer keeps the
// set frozen for the lifetime of the connection (slice 6).
func (s *server) buildOwnedAppCache(ctx context.Context, accountID string) map[string]struct{} {
	apps, err := s.store.ListApps(ctx, accountID)
	if err != nil {
		return map[string]struct{}{}
	}
	out := make(map[string]struct{}, len(apps))
	for _, a := range apps {
		out[a.ID] = struct{}{}
	}
	return out
}

// normalizedEventsFrameForAccount also upgrades a legacy raw app_changed UUID
// to the canonical JSON envelope before it reaches a customer-visible stream.
func normalizedEventsFrameForAccount(n db.Notification, accountID string, apps map[string]struct{}) (db.Notification, bool) {
	if n.Channel == db.NotifyAppChanged {
		payload, err := db.ParseAppChangedPayload(n.Payload)
		if err != nil {
			return db.Notification{}, false
		}
		if payload.AccountID != "" {
			if payload.AccountID != accountID {
				return db.Notification{}, false
			}
		} else if _, ok := apps[payload.AppID]; !ok {
			return db.Notification{}, false
		}
		if payload.Legacy {
			wire, err := db.MarshalAppChangedPayload(payload)
			if err != nil {
				return db.Notification{}, false
			}
			n.Payload = string(wire)
		}
		return n, true
	}

	var f struct {
		AppID     string `json:"app_id"`
		AccountID string `json:"account_id"`
	}
	if err := json.Unmarshal([]byte(n.Payload), &f); err != nil {
		// Unparseable — drop. Logging is the caller's job (the SSE
		// handler logs every frame; this filter only decides drop/deliver).
		return db.Notification{}, false
	}
	if f.AccountID != "" {
		return n, f.AccountID == accountID
	}
	if f.AppID != "" {
		_, ok := apps[f.AppID]
		return n, ok
	}
	// Orphan: no app_id, no account_id. Drop — same reasoning as above.
	return db.Notification{}, false
}

// writeSSEFrame writes one pg_notify payload as an SSE frame. Event
// name is the channel; data is the verbatim JSON payload.
func writeSSEFrame(w http.ResponseWriter, n db.Notification) {
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", n.Channel, n.Payload)
}
