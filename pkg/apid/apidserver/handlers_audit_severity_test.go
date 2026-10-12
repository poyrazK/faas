// Severity projection tests for auditEventResponse (Mega-PR B).
// Split from handlers_audit_test.go so the wire-shape addition
// has its own focused surface; the existing handler tests stay
// focused on the listing/filtering contract.
//
// Pins:
//   - auditEventResponse hoists data.severity onto the top-level
//     Severity field on the JSON wire shape (api.AuditEventResponse)
//   - non-stateless kinds (no data.severity key) render with no
//     Severity field at all — the omitempty contract
//   - pre-PR-427 stateless.advisory rows (data.severity absent)
//     also render empty — backwards-compat for existing audit data
//
// The tests drive auditEventResponse directly (it is an
// unexported helper, so whitebox-only — package main).

package apidserver_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestAuditEventResponse_ProjectsDataSeverity pins the happy
// path: a stateless.advisory row whose data carries severity="high"
// renders AuditEventResponse.Severity == "high".
func TestAuditEventResponse_ProjectsDataSeverity(t *testing.T) {
	dataJSON := []byte(`{"instance":"i-1","app_id":"a-1","count":1,"events":[],"severity":"high"}`)
	subject := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	e := state.Event{
		ID:      42,
		At:      time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC),
		Actor:   "apid",
		Kind:    "stateless.advisory",
		Subject: &subject,
		Data:    dataJSON,
	}
	resp := apidserver.AuditEventResponse(e)
	if resp.Severity != "high" {
		t.Errorf("Severity = %q, want %q", resp.Severity, "high")
	}
	if resp.Kind != "stateless.advisory" {
		t.Errorf("Kind = %q, want stateless.advisory", resp.Kind)
	}
	if string(resp.Data) != string(dataJSON) {
		t.Errorf("Data not preserved: got %q, want %q", resp.Data, dataJSON)
	}
}

// TestAuditEventResponse_EmptySeverityForNonStatelessKinds pins
// the "no severity" path for non-stateless kinds: when the data
// JSONB doesn't have a "severity" key, resp.Severity must be the
// zero value (empty string), so the omitempty tag suppresses it
// from the wire entirely.
func TestAuditEventResponse_EmptySeverityForNonStatelessKinds(t *testing.T) {
	dataJSON := []byte(`{"ip":"10.0.0.1","ua":"curl/8.0"}`)
	e := state.Event{
		ID:    43,
		At:    time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC),
		Actor: "apid",
		Kind:  "auth.login",
		Data:  dataJSON,
	}
	resp := apidserver.AuditEventResponse(e)
	if resp.Severity != "" {
		t.Errorf("Severity = %q, want \"\" (non-stateless kinds must render no Severity)", resp.Severity)
	}

	// And on the JSON wire the field must be absent (omitempty).
	wire, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(wire), `"severity"`) {
		t.Errorf("wire shape leaks severity field for non-stateless kind: %s", wire)
	}
}

// TestAuditEventResponse_PrePR427RowsRenderEmpty pins the
// backwards-compat contract: stateless.advisory rows written
// before commit 5 (data.severity was absent in PR #427's emit
// gap) render with Severity="" so the JSON wire omits the field
// entirely. Customers with audit data already in the table must
// not see a wire-shape change for those rows.
func TestAuditEventResponse_PrePR427RowsRenderEmpty(t *testing.T) {
	// Pre-PR-427 shape: data has instance/app_id/count/events but
	// NO "severity" key. This is the shape the apid receiver
	// wrote before commit 5; commit 5 fixes the gap on the
	// emit side, but rows already in the table retain this shape.
	dataJSON := []byte(`{"instance":"i-1","app_id":"a-1","count":1,"events":[]}`)
	e := state.Event{
		ID:    44,
		At:    time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC),
		Actor: "apid",
		Kind:  "stateless.advisory",
		Data:  dataJSON,
	}
	resp := apidserver.AuditEventResponse(e)
	if resp.Severity != "" {
		t.Errorf("Severity = %q, want \"\" (pre-PR-427 row must not error or guess)", resp.Severity)
	}
	wire, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(wire), `"severity"`) {
		t.Errorf("wire shape leaks severity for pre-PR-427 row: %s", wire)
	}
}

// TestAuditEventResponse_AllSeveritiesProject pins the closed
// vocabulary: high / warn / info all flow through unchanged.
// Future vocabulary additions must update both dataSeverity's
// switch and the receiver's severityForPath to keep them aligned.
func TestAuditEventResponse_AllSeveritiesProject(t *testing.T) {
	for _, sev := range []string{"high", "warn", "info"} {
		t.Run(sev, func(t *testing.T) {
			dataJSON := []byte(`{"severity":"` + sev + `"}`)
			e := state.Event{
				ID: 1, At: time.Now(), Actor: "apid",
				Kind: "stateless.advisory", Data: dataJSON,
			}
			resp := apidserver.AuditEventResponse(e)
			if resp.Severity != sev {
				t.Errorf("Severity = %q, want %q", resp.Severity, sev)
			}
		})
	}
}
