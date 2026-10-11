// Counter-increment tests for the advisory receiver (Mega-PR B).
// Split from advisory_receiver_test.go so the metric wiring has its
// own surface and the existing happy-path tests stay focused on the
// audit / pg_notify contract.
//
// Pins:
//   - apid_stateless_advisory_events_total{severity} increments by 1
//     per ForwardStatelessAdvisory call, labelled by the receiver's
//     advisoryBatchSeverity result.
//   - Unknown / unexpected severity values are guarded by the
//     closed-set switch in ObserveStatelessAdvisory (parity with
//     the vmmd-side counter — see pkg/wire/metrics.go).
//   - Nil *OpsMetrics is a no-op so the receiver's existing
//     nil-tolerance contract is preserved.

package apidserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestAdvisoryReceiver_IncrementsCounterBySeverity (Mega-PR B) —
// drive three calls (one per severity) and assert each counter
// increments by 1. Uses the same /metrics scrape seam as
// pkg/vmmdgrpc/advisory_client_test.go so the wire-level format is
// pinned too.
func TestAdvisoryReceiver_IncrementsCounterBySeverity(t *testing.T) {
	ops := wire.NewOpsMetrics("apid")
	store := &advisoryStubStore{app: state.App{AccountID: "acct-1"}}
	audit := &advisoryStubAudit{}
	notif := &advisoryStubNotifier{}
	rcv := newAdvisoryReceiver(store, audit, notif)
	rcv.ops = ops

	cases := []struct {
		name     string
		event    *apidpb.AdvisoryEvent
		wantLine string
	}{
		{
			name:     "high",
			event:    &apidpb.AdvisoryEvent{Path: "/data/foo", Pid: 42, TsUnixMs: 1},
			wantLine: `apid_stateless_advisory_events_total{severity="high"}`,
		},
		{
			name:     "warn",
			event:    &apidpb.AdvisoryEvent{Path: "/etc/passwd", Pid: 42, TsUnixMs: 1},
			wantLine: `apid_stateless_advisory_events_total{severity="warn"}`,
		},
		{
			name:     "info (empty batch)",
			event:    nil, // empty events list → severityInfo
			wantLine: `apid_stateless_advisory_events_total{severity="info"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events := []*apidpb.AdvisoryEvent{}
			if tc.event != nil {
				events = append(events, tc.event)
			}
			req := &apidpb.ForwardStatelessAdvisoryRequest{
				Instance: "i-1", AppId: "a-1", Events: events,
			}
			if _, err := rcv.ForwardStatelessAdvisory(context.Background(), req); err != nil {
				t.Fatalf("ForwardStatelessAdvisory: %v", err)
			}
		})
	}

	// Each label should be exactly 1 — the only increments are the
	// three above. Pre-instantiation at boot put every row at 0.
	wantLines := []string{
		`apid_stateless_advisory_events_total{severity="high"} 1`,
		`apid_stateless_advisory_events_total{severity="warn"} 1`,
		`apid_stateless_advisory_events_total{severity="info"} 1`,
	}
	body := scrapeOpsMetrics(t, ops)
	for _, want := range wantLines {
		if !strings.Contains(body, want) {
			t.Errorf("missing line %q in:\n%s", want, body)
		}
	}
}

// TestAdvisoryReceiver_NilOpsMetricsNoCrash pins the nil-safe
// contract: a receiver with a nil *OpsMetrics must not panic when
// an advisory lands. The audit row writes normally; the counter
// increment is a no-op.
func TestAdvisoryReceiver_NilOpsMetricsNoCrash(t *testing.T) {
	store := &advisoryStubStore{app: state.App{AccountID: "acct-1"}}
	audit := &advisoryStubAudit{}
	notif := &advisoryStubNotifier{}
	rcv := newAdvisoryReceiver(store, audit, notif)
	// rcv.ops is the zero value (nil). No panic expected.

	req := &apidpb.ForwardStatelessAdvisoryRequest{
		Instance: "i-1", AppId: "a-1",
		Events: []*apidpb.AdvisoryEvent{{Path: "/data/foo", Pid: 42, TsUnixMs: 1}},
	}
	if _, err := rcv.ForwardStatelessAdvisory(context.Background(), req); err != nil {
		t.Fatalf("ForwardStatelessAdvisory with nil ops: %v", err)
	}
	if got := audit.callCount(); got != 1 {
		t.Errorf("audit.Emit count = %d, want 1 (nil ops must not skip the audit row)", got)
	}
}

// TestAdvisoryReceiver_CounterAfterNotFound — the codes.NotFound
// path (missing app row) does NOT increment the counter. The
// receiver returns early before reaching the audit emit / counter
// branch, so the pair-counter (apid events vs. vmmd ok) stays
// consistent: a NotFound from vmmd's perspective is a
// "transiently-down apid" retry case, not a "landed advisory" case.
func TestAdvisoryReceiver_CounterAfterNotFound(t *testing.T) {
	ops := wire.NewOpsMetrics("apid")
	store := &advisoryStubStore{err: state.ErrNotFound}
	audit := &advisoryStubAudit{}
	notif := &advisoryStubNotifier{}
	rcv := newAdvisoryReceiver(store, audit, notif)
	rcv.ops = ops

	req := &apidpb.ForwardStatelessAdvisoryRequest{
		Instance: "i-1", AppId: "a-gone",
		Events: []*apidpb.AdvisoryEvent{{Path: "/data/foo", Pid: 42, TsUnixMs: 1}},
	}
	_, err := rcv.ForwardStatelessAdvisory(context.Background(), req)
	if err == nil {
		t.Fatal("expected NotFound error, got nil")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.NotFound {
		t.Errorf("code = %v (ok=%v), want NotFound", st.Code(), ok)
	}
	// All three counters must stay at 0.
	body := scrapeOpsMetrics(t, ops)
	for _, sev := range []string{"high", "warn", "info"} {
		want := `apid_stateless_advisory_events_total{severity="` + sev + `"} 0`
		if !strings.Contains(body, want) {
			t.Errorf("NotFound path incremented %s, body:\n%s", want, body)
		}
	}
}

// scrapeOpsMetrics fetches the /metrics body of ops via httptest.
// Mirrors the helper in pkg/vmmdgrpc/advisory_client_test.go (kept
// private to each package so cmd/apid tests don't take a dependency
// on pkg/vmmdgrpc).
func scrapeOpsMetrics(t *testing.T, ops *wire.OpsMetrics) string {
	t.Helper()
	srv := httptest.NewServer(ops.Handler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("scrape body: %v", err)
	}
	return string(bodyBytes)
}
