// commands_audit_events_test.go — Move 1 PR-A: tests for the new
// --verbose flag on `gregale audit-events`. Mirrors the
// commands_metrics_test.go shape: httptest fake-apid + t.Setenv
// for FAAS_API / FAAS_TOKEN + osStdout seam for human-mode asserts.
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// captureAuditStdout swaps os.Stdout for a pipe for the duration of
// the test, returning the captured bytes. cmdAuditEvents uses bare
// fmt.Printf (not the osStdout seam) so we have to redirect at the
// os.Stdout level. Renamed from captureStdout to avoid colliding
// with commands5_test.go's same-named helper.
func captureAuditStdout(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	oldOut := os.Stdout
	os.Stdout = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	return func() string {
		_ = w.Close()
		os.Stdout = oldOut
		<-done
		return buf.String()
	}
}

// advisoryEventData is the shape cmd/apid/advisory_receiver.go
// emits as the Data field of a stateless.advisory audit row. Kept
// here as a local type so the test doesn't grow a dependency on
// the apid package.
func advisoryEventData() []byte {
	b, _ := json.Marshal(map[string]any{
		"instance": "i-abc123",
		"app_id":   "app-uuid-456",
		"count":    4,
		"events": []map[string]any{
			{"path": "/data/foo", "mask": []string{"create", "modify"}, "pid": 4242, "ts_unix_ms": int64(1722200000000)},
			{"path": "/data/bar", "mask": []string{"create"}, "pid": 4242, "ts_unix_ms": int64(1722200001000)},
		},
	})
	return b
}

// TestCmdAuditEvents_Verbose_ExpandedColumns: --verbose rewrites
// stateless.advisory rows into the 5-column expanded view:
// instance | count | paths | sample_pid | last_ts. Pinned against
// the advisory row's Data shape so a future drift in
// advisory_receiver.go fails the test loudly.
func TestCmdAuditEvents_Verbose_ExpandedColumns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.ListAuditEventsResponse{
			Events: []api.AuditEventResponse{
				{At: "2026-07-28T12:00:00Z", Actor: "guest-init@i-abc", Kind: "stateless.advisory", Subject: "app-uuid-456", Data: advisoryEventData()},
			},
		})
	}))
	defer srv.Close()

	stop := captureAuditStdout(t)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdAuditEvents([]string{"list", "--kind-prefix", "stateless.advisory", "--verbose"}); code != 0 {
		t.Fatalf("audit-events --verbose = %d, want 0", code)
	}
	out := stop()
	// 5 columns separated by \t: at, instance, count, paths, pid, last_ts
	// (six visible fields because the timestamp column sits before instance).
	for _, want := range []string{
		"i-abc123", // instance
		"4",        // count
		"/data/foo,/data/bar",
		"4242",       // sample pid
		"2026-07-28", // last_ts formatted
	} {
		if !strings.Contains(out, want) {
			t.Errorf("verbose output missing %q\nfull: %s", want, out)
		}
	}
}

// TestCmdAuditEvents_Verbose_NonStatelessFallsBack: --verbose only
// rewrites stateless.advisory rows. Other kinds must keep the
// 4-column shape (at, actor, kind, subject) so an operator
// running --verbose against the full audit log still gets a
// readable table.
func TestCmdAuditEvents_Verbose_NonStatelessFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.ListAuditEventsResponse{
			Events: []api.AuditEventResponse{
				{At: "2026-07-28T12:00:00Z", Actor: "schedd", Kind: "app.scaled", Subject: "app-uuid-456"},
			},
		})
	}))
	defer srv.Close()

	stop := captureAuditStdout(t)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdAuditEvents([]string{"list", "--verbose"}); code != 0 {
		t.Fatalf("audit-events --verbose (non-stateless row) = %d, want 0", code)
	}
	out := stop()
	// 4-col shape: at TAB actor TAB kind TAB subject.
	for _, want := range []string{"2026-07-28T12:00:00Z", "schedd", "app.scaled", "app-uuid-456"} {
		if !strings.Contains(out, want) {
			t.Errorf("default 4-col output missing %q\nfull: %s", want, out)
		}
	}
	// And must NOT carry the expanded columns.
	for _, mustNot := range []string{"i-abc123", "/data/foo"} {
		if strings.Contains(out, mustNot) {
			t.Errorf("non-stateless row rendered as expanded; contains %q\nfull: %s", mustNot, out)
		}
	}
}

// TestCmdAuditEvents_DefaultShapeUnchanged: without --verbose, the
// existing 4-column shape (at, actor, kind, subject) is preserved.
// Regression guard for the verbose flag being a strict superset.
func TestCmdAuditEvents_DefaultShapeUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.ListAuditEventsResponse{
			Events: []api.AuditEventResponse{
				{At: "2026-07-28T12:00:00Z", Actor: "guest-init", Kind: "stateless.advisory", Subject: "app-uuid-456", Data: advisoryEventData()},
			},
		})
	}))
	defer srv.Close()

	stop := captureAuditStdout(t)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdAuditEvents([]string{"list"}); code != 0 {
		t.Fatalf("audit-events default = %d, want 0", code)
	}
	out := stop()
	// Default 4-col shape.
	if !strings.Contains(out, "2026-07-28T12:00:00Z\tguest-init\tstateless.advisory\tapp-uuid-456") {
		t.Errorf("default 4-col line missing\nfull: %s", out)
	}
}

func TestCmdAuditEvents_JSONEmitsCompleteNDJSONRows(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = false })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.ListAuditEventsResponse{Events: []api.AuditEventResponse{{
			ID: "event-1", At: "2026-09-10T17:51:28Z", Actor: "schedd",
			Kind: "wake.admitted", Subject: "account-1", Data: json.RawMessage(`{"app_id":"app-1"}`),
		}}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	out, restore := swapStdout(t)
	defer restore()
	if code := cmdAuditEvents([]string{"list"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var row api.AuditEventResponse
	if err := json.Unmarshal(out.Bytes(), &row); err != nil {
		t.Fatalf("NDJSON row: %v; output=%q", err, out.String())
	}
	if row.ID != "event-1" || !strings.Contains(string(row.Data), "app-1") {
		t.Fatalf("row = %+v", row)
	}
}
