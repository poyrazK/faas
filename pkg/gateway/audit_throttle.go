package gateway

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"
)

// auditRepeatWindow is how often one informational audit row repeats for the
// same rule or instance. edge_rule.*_matched and vm.inflight_threshold_reached
// wrote one control-plane events row per request: at 300 req/s an app with a
// CORS rule was 96 inserts/s, the largest per-request write on the CP
// (2026-10-31). Metrics still count every occurrence.
const auditRepeatWindow = time.Minute

// auditRepeatKeys bounds the keys one throttle tracks. Past it, expired keys
// are swept; if every key is live, rows pass through unthrottled.
const auditRepeatKeys = 4096

// repeatThrottledAuditor forwards the first informational row per key, then
// at most one per auditRepeatWindow, carrying how many it held back as
// data["suppressed"]; a count still held when its series goes quiet is
// dropped with the key. The denials a client produces at will (a throttle's
// 429, a validate or limit refusal, a forged caller address, a missing
// throttle identity) repeat per rule the same way (IsRepeatedDenialAudit):
// one row per request let a flood against a throttle rule turn into one
// control-plane insert per refused request and crowd other tenants' evidence
// out of the node's audit queue (ADR-091 amendment 2026-11-04, round 11).
// The sampled security events (ADR-964) and metrics still see each request.
// Every other kind passes through unchanged.
type repeatThrottledAuditor struct {
	next EdgeRuleAuditor
	now  func() time.Time

	mu   sync.Mutex
	keys map[string]*auditRepeat
}

type auditRepeat struct {
	until      time.Time
	suppressed int64
}

// throttleRepeatedAudit wraps next; a nil next stays nil so callers keep
// their audit-disabled checks.
func throttleRepeatedAudit(next EdgeRuleAuditor) EdgeRuleAuditor {
	if next == nil {
		return nil
	}
	return &repeatThrottledAuditor{next: next, now: time.Now, keys: make(map[string]*auditRepeat)}
}

func (t *repeatThrottledAuditor) Emit(ctx context.Context, kind string, subject *string, data map[string]any) {
	key, repeats := auditRepeatKey(kind, data)
	if !repeats {
		t.next.Emit(ctx, kind, subject, data)
		return
	}
	suppressed, emit := t.admit(key)
	if !emit {
		return
	}
	if suppressed > 0 {
		data = maps.Clone(data)
		if data == nil {
			data = make(map[string]any, 1)
		}
		data["suppressed"] = suppressed
	}
	t.next.Emit(ctx, kind, subject, data)
}

// auditRepeatKey names the series an informational row belongs to.
func auditRepeatKey(kind string, data map[string]any) (string, bool) {
	switch {
	case kind == "vm.inflight_threshold_reached":
		return kind + "|" + fmt.Sprint(data["instance_id"]), true
	case IsInformationalAudit(kind):
		return kind + "|" + fmt.Sprint(data["rule_id"]) + "|" + fmt.Sprint(data["preflight"]), true
	case IsRepeatedDenialAudit(kind):
		return kind + "|" + fmt.Sprint(data["rule_id"]) + "|" + fmt.Sprint(data["app_id"]), true
	default:
		return "", false
	}
}

// IsInformationalAudit reports a request-path audit kind that records that
// something matched, not evidence of a refusal: edge_rule.*_matched and
// vm.inflight_threshold_reached. Such rows are throttled here and queued
// behind evidence rows (authn failures, denials) by gatewayd-internal's
// audit writer, so a flood of matches cannot displace them (O22).
func IsInformationalAudit(kind string) bool {
	return kind == "vm.inflight_threshold_reached" || kind == "edge_rule.async_enqueued" ||
		(strings.HasPrefix(kind, "edge_rule.") && strings.HasSuffix(kind, "_matched"))
}

// repeatedDenialAudits are the refusals a client can produce once per
// request. They stay evidence (queued ahead of informational rows) but repeat
// at most once per auditRepeatWindow per rule and app.
var repeatedDenialAudits = map[string]struct{}{
	"edge_rule.throttle_rejected":               {},
	"edge_rule.throttle_identity_missing":       {},
	"edge_rule.validate_failed":                 {},
	"edge_rule.validate_unsupported_media_type": {},
	"edge_rule.limit_rejected":                  {},
	"edge_rule.caller_ip_forged":                {},
	"edge_rule.ingress_ip_forged":               {},
}

// IsRepeatedDenialAudit reports a refusal audit kind that repeats per rule
// and app rather than per request.
func IsRepeatedDenialAudit(kind string) bool {
	_, ok := repeatedDenialAudits[kind]
	return ok
}

func (t *repeatThrottledAuditor) admit(key string) (suppressed int64, emit bool) {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if r := t.keys[key]; r != nil {
		if now.Before(r.until) {
			r.suppressed++
			return 0, false
		}
		suppressed, r.suppressed, r.until = r.suppressed, 0, now.Add(auditRepeatWindow)
		return suppressed, true
	}
	if len(t.keys) >= auditRepeatKeys {
		for k, r := range t.keys {
			if !now.Before(r.until) {
				delete(t.keys, k)
			}
		}
		if len(t.keys) >= auditRepeatKeys {
			return 0, true
		}
	}
	t.keys[key] = &auditRepeat{until: now.Add(auditRepeatWindow)}
	return 0, true
}
