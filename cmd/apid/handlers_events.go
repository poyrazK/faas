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
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// eventsChannels is the set we subscribe to. Slice 6 keeps it flat;
// slice 9 (CLI smoke) widens if a notify is needed that isn't here.
var eventsChannels = []string{
	db.NotifyAppChanged,
	db.NotifyDeploymentChanged,
	db.NotifyInstanceChanged,
	db.NotifyCronFired,
	db.NotifyQuotaWarning,
	db.NotifyBillingPastDue,
}

// eventsHandler is the SSE handler. It accepts either a session cookie
// (dashboard) or an API key (CLI), resolves the account, then dumps
// every relevant pg_notify frame to the client as `event: <kind>`
// frames until the client disconnects.
func (s *server) eventsHandler(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		acct, ok := resolveEventsCaller(r, s)
		if !ok {
			api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
				"Unauthorized", "session cookie or API key required"))
			return
		}
		ownedApps := s.buildOwnedAppCache(r.Context(), acct.ID)

		startSSE(w)
		flusher, _ := w.(http.Flusher)

		ch, cancel, err := s.notif.Subscribe(r.Context(), eventsChannels)
		if err != nil {
			_, _ = fmt.Fprintf(w, "event: error\ndata: {\"message\":%q}\n\n", err.Error())
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
				if !eventsFrameForAccount(n, acct.ID, ownedApps) {
					continue
				}
				writeSSEFrame(w, n)
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
// an API key (CLI), pulling the matching account off the request.
// Returns the account + true; or false if neither auth path matched.
func resolveEventsCaller(r *http.Request, s *server) (state.Account, bool) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if env, err := s.sessions.Verify(c.Value); err == nil {
			if acct, err := s.store.AccountByID(r.Context(), env.AccountID); err == nil && acct.Active() {
				return acct, true
			}
		}
	}
	if h := r.Header.Get("Authorization"); len(h) > 7 && h[:7] == "Bearer " {
		tok := h[7:]
		if api.ValidAPIKeyFormat(tok) {
			if acct, err := s.store.AccountByKeyHash(r.Context(), api.HashAPIKey(tok)); err == nil && acct.Active() {
				return acct, true
			}
		}
	}
	return state.Account{}, false
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

// eventsFrameForAccount filters notifications down to those that
// concern this account. The pg_notify payload is JSON with optional
// `app_id` (string uuid) and `account_id` (string uuid) fields.
//
// Failure mode: refuse-to-decide. Unparseable JSON or payloads
// without an `app_id` or `account_id` are dropped — we cannot
// prove the frame belongs to this account, so the privacy-safe
// default is to not deliver it. (Was previously fail-open: any
// unparseable or anonymous frame was sent to every connection,
// which leaked cross-account notifications on a one-box.)
func eventsFrameForAccount(n db.Notification, accountID string, apps map[string]struct{}) bool {
	var f struct {
		AppID     string `json:"app_id"`
		AccountID string `json:"account_id"`
	}
	if err := json.Unmarshal([]byte(n.Payload), &f); err != nil {
		// Unparseable — drop. Logging is the caller's job (the SSE
		// handler logs every frame; this filter only decides drop/deliver).
		return false
	}
	if f.AccountID != "" {
		return f.AccountID == accountID
	}
	if f.AppID != "" {
		_, ok := apps[f.AppID]
		return ok
	}
	// Orphan: no app_id, no account_id. Drop — same reasoning as above.
	return false
}

// writeSSEFrame writes one pg_notify payload as an SSE frame. Event
// name is the channel; data is the verbatim JSON payload.
func writeSSEFrame(w http.ResponseWriter, n db.Notification) {
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", n.Channel, n.Payload)
}
