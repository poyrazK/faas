// openapi_doc_subscriber.go — ADR-126 / issue #975 item #2:
//
// subscriber bridge for the `?source=auto` OpenAPI cache.
//
// The auto-gen spec (handler_app_openapi.go) is cached per
// (app_id, sha(doc), sha(routes), sha(rules)) in
// pkg/openapidiff.SpecCache. Three event sources mutate those
// three inputs:
//
//   1. NotifyAppOpenAPIDocChanged (NEW, item #2 D5) — fired
//      by postAppOpenAPIImport / deleteAppOpenAPIImport on
//      create / replace / delete. Payload: {"app_id":..., "op":...}.
//
//   2. NotifyEdgeRuleChanged (EXISTING, item #1 / ADR-091) —
//      fired by the createEdgeRule / updateEdgeRule /
//      deleteEdgeRule paths on create / update / delete.
//      Payload shape matches the existing
//      NotifyEdgeRuleChanged decoder in pkg/state.
//
// The subscriber listens to BOTH channels and flushes
// InvalidateByApp(appID) on every notification. Wholesale
// per-app flush is correct because (a) the cache key already
// embeds the SHA of each input, so a no-op rewrite produces
// the same key (a cache hit) and would not need invalidation;
// and (b) the cache is bounded at 256 entries so the per-app
// delete is O(appKeys) cheap.
//
// We use SubscribeWithReconnect (the same wrapper the
// audit_subscriber uses) so this loop survives Postgres
// restarts. The initial Subscribe error is fatal — silent
// drop is the bug this PR is closing.
//
// Lifecycle: runOpenAPIDocSubscriber is invoked once from
// main.go's bgBefore and lives for the daemon's lifetime.
// The ctx that cancels the daemon also cancels the
// subscriber.
package main

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/openapidiff"
)

// openAPIImportPayload mirrors the JSON shape apid emits on
// the NotifyAppOpenAPIDocChanged channel. The decoder is
// intentionally narrow — anything outside the named fields
// is dropped at decode time so a future producer-side field
// addition doesn't accidentally fan out into the cache.
type openAPIImportPayload struct {
	AppID string `json:"app_id"`
	Op    string `json:"op"`
}

// edgeRuleChangedPayload mirrors the JSON shape the existing
// createEdgeRule / updateEdgeRule / deleteEdgeRule paths
// emit. We only need the app_id field for the cache flush.
type edgeRuleChangedPayload struct {
	AppID  string `json:"app_id"`
	RuleID string `json:"rule_id"`
	Op     string `json:"op"`
}

// runOpenAPIDocSubscriber subscribes to the two pg_notify
// channels that mutate the `?source=auto` cache inputs and
// flushes the cache per-app on each notification. The pool
// is the live pgx.Pool that SubscribeWithReconnect holds
// open; cache is the in-process LRU wired from main.go.
//
// The function returns when ctx is cancelled. The initial
// Subscribe error is fatal — silent drop is the bug this
// PR is closing (mirrors runAuditSubscriber).
func runOpenAPIDocSubscriber(ctx context.Context, pool *pgxpool.Pool, cache *openapidiff.SpecCache, log *slog.Logger) error {
	if cache == nil {
		// Defensive: production always wires a non-nil cache,
		// but a misconfigured dev box (or a test harness) would
		// otherwise spawn a goroutine that loops forever doing
		// nothing. Bail out at boot with a clear log line.
		log.Warn("openapi_doc: subscriber skipped, no cache wired")
		return nil
	}
	channels := []string{
		db.NotifyAppOpenAPIDocChanged,
		db.NotifyEdgeRuleChanged,
	}
	ch, err := db.SubscribeWithReconnect(ctx, pool, channels, log)
	if err != nil {
		return err
	}
	log.Info("openapi_doc: subscriber started", "channels", channels)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case n, ok := <-ch:
			if !ok {
				// Channel closed by the reconnect wrapper on a
				// Postgres restart; the inner loop reconnects,
				// so the outer loop just exits cleanly.
				return nil
			}
			if n.Payload == "" {
				continue
			}
			appID := appIDFromPayload(n.Channel, n.Payload)
			if appID == "" {
				log.Warn("openapi_doc: bad payload", "channel", n.Channel)
				continue
			}
			cache.InvalidateByApp(appID)
			log.Debug("openapi_doc: cache invalidated", "channel", n.Channel, "app_id", appID)
		}
	}
}

// appIDFromPayload decodes the app_id field from either
// channel's payload shape. Returns "" on any decode error so
// the caller skips the notification (the cache state stays
// valid; a missed flush falls off the LRU via TTL).
func appIDFromPayload(channel, payload string) string {
	switch channel {
	case db.NotifyAppOpenAPIDocChanged:
		var p openAPIImportPayload
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			return ""
		}
		return p.AppID
	case db.NotifyEdgeRuleChanged:
		var p edgeRuleChangedPayload
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			return ""
		}
		return p.AppID
	default:
		return ""
	}
}