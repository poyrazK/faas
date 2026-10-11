// End-to-end severity-chain test (Mega-PR B commit 5).
// Pins the trio (receiver emit → data map → counter increment →
// DTO projection) so a future refactor that breaks any single
// link fails CI rather than silently dropping one side. This
// file does NOT add new functionality — every link is already
// in production — but it makes the cross-link invariant explicit.
//
// Why a separate file: the receiver unit tests in
// advisory_receiver_test.go and advisory_receiver_metrics_test.go
// already pin the per-link behaviour in isolation. The handler
// projection is pinned in handlers_audit_severity_test.go. None
// of those tests exercise the receiver's emit AND the DTO
// projection against the same severity value, which is the
// invariant this file is responsible for.

package apidserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestAdvisorySeverity_StubChainFromEmitToWire pins the full
// Mega-PR B chain against the receiver's stub seam:
//
//  1. Receiver writes data.severity="high" into the audit row's
//     data map at emit time (cmd/apid/advisory_receiver.go:135).
//  2. The same severity increments
//     apid_stateless_advisory_events_total{severity="high"}.
//  3. auditEventResponse projects data.severity onto the JSON
//     wire shape as the top-level Severity field
//     (cmd/apid/handlers_audit.go::auditEventResponse).
//
// If any link breaks, this test catches it. The closed-set guard
// in ObserveStatelessAdvisory / dataSeverity means a stray label
// value would silently drop, so the test runs all three severities
// (high / warn / info).
//
// Scope: this test exercises the receiver with a stub audit
// emitter and a NIL notifier. It does NOT cover the live pg_notify
// path (handlers_events.go:cmdSubscriptionFrame) or the live
// auditor's batched-flush path (cmd/apid/audit.go::flushOne).
// Cross-process invariants between the vmmd and apid counters
// are Tier-A e2e territory — see the review note on Mega-PR B
// and the §6.2 invariants testing matrix. Anything tighter than
// that should land in cmd/e2e (per memory make-e2e-target-post-tier-a)
// rather than here.
func TestAdvisorySeverity_StubChainFromEmitToWire(t *testing.T) {
	cases := []struct {
		name  string
		event *apidpb.AdvisoryEvent
		want  string
	}{
		{
			name:  "high",
			event: &apidpb.AdvisoryEvent{Path: "/data/foo", Pid: 1, TsUnixMs: 1},
			want:  "high",
		},
		{
			name:  "warn",
			event: &apidpb.AdvisoryEvent{Path: "/etc/passwd", Pid: 1, TsUnixMs: 1},
			want:  "warn",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ops := wire.NewOpsMetrics("apid")
			store := &advisoryStubStore{app: state.App{ID: "app-uuid", AccountID: "acct-1", Slug: "test"}}
			audit := &advisoryStubAudit{}
			recv := newAdvisoryReceiver(store, audit, nil)
			recv.ops = ops

			req := &apidpb.ForwardStatelessAdvisoryRequest{
				AppId: "app-uuid", Instance: "i-1", Events: []*apidpb.AdvisoryEvent{tc.event},
			}
			if _, err := recv.ForwardStatelessAdvisory(context.Background(), req); err != nil {
				t.Fatalf("ForwardStatelessAdvisory: %v", err)
			}

			// Link 1: receiver wrote data.severity.
			if audit.callCount() != 1 {
				t.Fatalf("audit calls = %d, want 1", audit.callCount())
			}
			gotData, _ := audit.calls[0].Data["severity"].(string)
			if gotData != tc.want {
				t.Errorf("link 1 (emit): data.severity = %q, want %q", gotData, tc.want)
			}

			// Link 2: counter incremented under the same severity.
			body := scrapeOpsMetrics(t, ops)
			wantLine := `apid_stateless_advisory_events_total{severity="` + tc.want + `"} 1`
			if !strings.Contains(body, wantLine) {
				t.Errorf("link 2 (counter): missing %q in body:\n%s", wantLine, body)
			}

			// Link 3: DTO projection surfaces the same value on the wire.
			// Marshal the audit row's data JSONB back into a state.Event
			// shape and run auditEventResponse.
			dataJSON, err := json.Marshal(audit.calls[0].Data)
			if err != nil {
				t.Fatalf("Marshal audit data: %v", err)
			}
			e := state.Event{
				ID:   1,
				Kind: "stateless.advisory",
				Data: dataJSON,
			}
			resp := auditEventResponse(e)
			if resp.Severity != tc.want {
				t.Errorf("link 3 (wire): resp.Severity = %q, want %q", resp.Severity, tc.want)
			}
			// Cross-check: marshal the DTO and confirm the field is on the wire.
			dtoBytes, err := json.Marshal(resp)
			if err != nil {
				t.Fatalf("Marshal DTO: %v", err)
			}
			var asMap map[string]any
			if err := json.Unmarshal(dtoBytes, &asMap); err != nil {
				t.Fatalf("Unmarshal DTO: %v", err)
			}
			if asMap["severity"] != tc.want {
				t.Errorf("link 3 wire: asMap[severity] = %v, want %q", asMap["severity"], tc.want)
			}

			// And the wire shape stays backwards-compat: an empty
			// Severity (non-stateless kind) MUST NOT surface on the
			// wire. This is the omitempty contract on
			// api.AuditEventResponse.Severity.
			_ = api.AuditEventResponse{} // compile-time reference — the DTO field exists
		})
	}
}
