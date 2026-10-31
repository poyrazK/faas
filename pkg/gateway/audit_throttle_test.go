package gateway

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type recordedAudit struct {
	kind string
	data map[string]any
}

type recordingAuditor struct {
	mu   sync.Mutex
	rows []recordedAudit
}

func (r *recordingAuditor) Emit(_ context.Context, kind string, _ *string, data map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, recordedAudit{kind: kind, data: data})
}

func TestRepeatThrottledAuditor(t *testing.T) {
	cors := func(rule string) map[string]any { return map[string]any{"rule_id": rule, "preflight": false} }
	for _, tc := range []struct {
		name           string
		kind           string
		data           func(i int) map[string]any
		emits          int
		advance        time.Duration // after the emits, then one more emit
		wantRows       int
		wantSuppressed any // on the last row, nil = key absent
	}{
		{name: "matched repeats are held back", kind: "edge_rule.cors_matched", data: func(int) map[string]any { return cors("r1") }, emits: 50, wantRows: 1},
		{name: "next window carries the count", kind: "edge_rule.cors_matched", data: func(int) map[string]any { return cors("r1") }, emits: 50, advance: auditRepeatWindow, wantRows: 2, wantSuppressed: int64(49)},
		{name: "rules are separate series", kind: "edge_rule.route_matched", data: func(i int) map[string]any { return cors(fmt.Sprint(i)) }, emits: 5, wantRows: 5},
		{name: "instances are separate series", kind: "vm.inflight_threshold_reached", data: func(i int) map[string]any { return map[string]any{"instance_id": fmt.Sprint(i % 2)} }, emits: 10, wantRows: 2},
		{name: "denials pass through", kind: "edge_rule.cors_blocked", data: func(int) map[string]any { return cors("r1") }, emits: 7, wantRows: 7},
		{name: "other kinds pass through", kind: "edge_rule.validate_failed", data: func(int) map[string]any { return cors("r1") }, emits: 3, wantRows: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingAuditor{}
			now := time.Unix(1_800_000_000, 0)
			audit := throttleRepeatedAudit(rec).(*repeatThrottledAuditor)
			audit.now = func() time.Time { return now }
			for i := range tc.emits {
				audit.Emit(context.Background(), tc.kind, nil, tc.data(i))
			}
			if tc.advance > 0 {
				now = now.Add(tc.advance)
				audit.Emit(context.Background(), tc.kind, nil, tc.data(0))
			}
			if len(rec.rows) != tc.wantRows {
				t.Fatalf("rows = %d, want %d", len(rec.rows), tc.wantRows)
			}
			if got := rec.rows[len(rec.rows)-1].data["suppressed"]; got != tc.wantSuppressed {
				t.Fatalf("suppressed = %v, want %v", got, tc.wantSuppressed)
			}
		})
	}
}

func TestRepeatThrottledAuditorKeepsCallerData(t *testing.T) {
	rec := &recordingAuditor{}
	now := time.Unix(1_800_000_000, 0)
	audit := throttleRepeatedAudit(rec).(*repeatThrottledAuditor)
	audit.now = func() time.Time { return now }
	data := map[string]any{"rule_id": "r1"}
	audit.Emit(context.Background(), "edge_rule.headers_matched", nil, data)
	audit.Emit(context.Background(), "edge_rule.headers_matched", nil, data)
	now = now.Add(auditRepeatWindow)
	audit.Emit(context.Background(), "edge_rule.headers_matched", nil, data)
	if _, ok := data["suppressed"]; ok {
		t.Fatalf("the caller's data map was modified")
	}
	if throttleRepeatedAudit(nil) != nil {
		t.Fatalf("a nil auditor must stay nil so audit-disabled checks hold")
	}
}

func TestRepeatThrottledAuditorIsBounded(t *testing.T) {
	rec := &recordingAuditor{}
	now := time.Unix(1_800_000_000, 0)
	audit := throttleRepeatedAudit(rec).(*repeatThrottledAuditor)
	audit.now = func() time.Time { return now }
	for i := range auditRepeatKeys + 10 {
		audit.Emit(context.Background(), "vm.inflight_threshold_reached", nil, map[string]any{"instance_id": fmt.Sprint(i)})
	}
	if len(audit.keys) > auditRepeatKeys {
		t.Fatalf("tracking %d keys, want at most %d", len(audit.keys), auditRepeatKeys)
	}
	if len(rec.rows) != auditRepeatKeys+10 {
		t.Fatalf("rows = %d; keys past the bound must pass through", len(rec.rows))
	}
	now = now.Add(auditRepeatWindow)
	audit.Emit(context.Background(), "vm.inflight_threshold_reached", nil, map[string]any{"instance_id": "new"})
	if len(audit.keys) != 1 {
		t.Fatalf("expired keys were not swept: %d left", len(audit.keys))
	}
}
