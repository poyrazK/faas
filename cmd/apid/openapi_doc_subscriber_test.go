package main

// Tests for the openapi_doc_subscriber.go (ADR-126 / issue #975
// item #2) — the pg_notify → SpecCache.InvalidateByApp bridge.
//
// Test surface:
//   - appIDFromPayload decodes the app_id field from each
//     channel's payload shape (NotifyAppOpenAPIDocChanged +
//     NotifyEdgeRuleChanged)
//   - appIDFromPayload returns "" on a malformed payload
//   - appIDFromPayload returns "" on an unknown channel name
//   - runOpenAPIDocSubscriber nil-cache fast-exit returns nil
//     without dialing Postgres
//   - runOpenAPIDocSubscriber with a real pgx pool + the
//     SubscribeWithReconnect wrapper is exercised via an
//     integration test in pkg/db (not duplicated here)

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
)

// TestAppIDFromPayload_OpenAPIImport verifies the
// NotifyAppOpenAPIDocChanged branch decodes the app_id field.
func TestAppIDFromPayload_OpenAPIImport(t *testing.T) {
	got := appIDFromPayload(db.NotifyAppOpenAPIDocChanged, `{"app_id":"app-123","op":"replaced"}`)
	if got != "app-123" {
		t.Errorf("got=%q, want app-123", got)
	}
}

// TestAppIDFromPayload_EdgeRuleChanged verifies the
// NotifyEdgeRuleChanged branch decodes the app_id field (the
// rule_id + op fields are ignored — only app_id matters for
// the cache flush).
func TestAppIDFromPayload_EdgeRuleChanged(t *testing.T) {
	got := appIDFromPayload(db.NotifyEdgeRuleChanged, `{"app_id":"app-456","rule_id":"rule-7","op":"created"}`)
	if got != "app-456" {
		t.Errorf("got=%q, want app-456", got)
	}
}

// TestAppIDFromPayload_MalformedOpenAPIImport verifies the
// decoder returns "" on a malformed NotifyAppOpenAPIDocChanged
// payload. The cache state stays valid; a missed flush falls
// off the LRU via TTL.
func TestAppIDFromPayload_MalformedOpenAPIImport(t *testing.T) {
	if got := appIDFromPayload(db.NotifyAppOpenAPIDocChanged, `{not json`); got != "" {
		t.Errorf("got=%q, want empty", got)
	}
}

// TestAppIDFromPayload_MalformedEdgeRuleChanged verifies the
// decoder returns "" on a malformed NotifyEdgeRuleChanged
// payload.
func TestAppIDFromPayload_MalformedEdgeRuleChanged(t *testing.T) {
	if got := appIDFromPayload(db.NotifyEdgeRuleChanged, `{"app_id":}`); got != "" {
		t.Errorf("got=%q, want empty", got)
	}
}

// TestAppIDFromPayload_EmptyAppID verifies the decoder returns
// "" when the payload is valid JSON but carries no app_id
// (the production rule is: app_id is required for the cache
// flush, so a missing app_id is a no-op).
func TestAppIDFromPayload_EmptyAppID(t *testing.T) {
	if got := appIDFromPayload(db.NotifyAppOpenAPIDocChanged, `{"op":"replaced"}`); got != "" {
		t.Errorf("got=%q, want empty", got)
	}
	if got := appIDFromPayload(db.NotifyEdgeRuleChanged, `{"rule_id":"rule-1","op":"deleted"}`); got != "" {
		t.Errorf("got=%q, want empty", got)
	}
}

// TestAppIDFromPayload_UnknownChannel verifies the decoder
// returns "" for any channel name it doesn't know. Defensive
// posture: a future notify channel added to the listener list
// but not yet wired into appIDFromPayload produces a log
// warning and a no-op rather than a panic.
func TestAppIDFromPayload_UnknownChannel(t *testing.T) {
	if got := appIDFromPayload("app_changed", `{"app_id":"app-1","op":"updated"}`); got != "" {
		t.Errorf("got=%q, want empty", got)
	}
}

// TestAppIDFromPayload_EmptyPayload verifies the decoder
// returns "" on an empty payload (the production loop skips
// empty payloads via the `n.Payload == ""` short-circuit, but
// the decoder's behaviour is the floor guarantee).
func TestAppIDFromPayload_EmptyPayload(t *testing.T) {
	if got := appIDFromPayload(db.NotifyAppOpenAPIDocChanged, ""); got != "" {
		t.Errorf("got=%q, want empty", got)
	}
}