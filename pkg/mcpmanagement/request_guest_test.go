// adr: 1627
package mcpmanagement

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func withRequestGuest(e api.DebugRequestEvidenceResponse) api.DebugRequestEvidenceResponse {
	e.Request.Guest = &api.DebugGuestExecutionEvidence{Runtime: "node22", DurationMS: 70, Outcome: "timeout", ErrorClass: "timeout"}
	// Keep the retained gap while describing the now-present guest signal.
	for i := range e.Correlation.Stages {
		if e.Correlation.Stages[i].Phase == "guest" {
			e.Correlation.Stages[i].Phase, e.Correlation.Stages[i].Reason = "queue", "no retained queue evidence"
		}
	}
	for i := range e.Explanation.Findings {
		if e.Explanation.Findings[i].Code == "evidence_gap" {
			e.Explanation.Findings[i].EvidenceRefs = []string{"correlation:queue"}
		}
	}
	e.Correlation.Stages = append(e.Correlation.Stages, api.DebugRequestCorrelationStage{Phase: "guest", Status: "observed", DurationMS: 70, Approximate: true})
	e.Explanation.Findings = append(e.Explanation.Findings, api.DebugEvidenceFinding{Code: "guest_failure", Title: "excluded-guest-title", Detail: "excluded-guest-error-text " + testToken, Confidence: "high", EvidenceRefs: []string{"guest"}})
	return e
}

func TestRequestGuestProjection(t *testing.T) {
	for _, mode := range []string{"observed", "zero", "missing", "missing_marker", "empty_outcome", "unknown_runtime", "unknown_outcome", "unknown_class", "negative_duration", "excess_duration", "mismatched_class", "ok_with_error", "journal", "empty_count", "aggregate"} {
		t.Run(mode, func(t *testing.T) {
			r := withRequestGuest(requestFixtureEvidence()).Request
			want := "observed"
			switch mode {
			case "zero":
				r.Guest.DurationMS, r.Guest.Outcome, r.Guest.ErrorClass = 0, "ok", ""
			case "missing":
				r.Guest, want = nil, "unavailable"
			case "missing_marker":
				r.Guest.Outcome, want = "missing", "unavailable"
			case "empty_outcome":
				r.Guest.Outcome, want = "", "unavailable"
			case "unknown_runtime":
				r.Guest.Runtime, want = "excluded-runtime "+testToken, "partial"
			case "unknown_outcome":
				r.Guest.Outcome, want = "excluded-outcome", "partial"
			case "unknown_class":
				r.Guest.ErrorClass, want = "excluded-error-text", "partial"
			case "negative_duration":
				r.Guest.DurationMS, want = -1, "invalid_native_guest"
			case "excess_duration":
				r.Guest.DurationMS, want = api.MCPRequestGuestDurationMaxMS+1, "invalid_native_guest"
			case "mismatched_class":
				r.Guest.ErrorClass, want = "http_5xx", "invalid_native_guest"
			case "ok_with_error":
				r.Guest.Outcome, want = "ok", "invalid_native_guest"
			case "journal":
				r.EvidenceStatus, want = "request_id_only", "unavailable"
			case "empty_count":
				r.Count, want = 0, "unavailable"
			case "aggregate":
				r.Count = 7
			}
			g := projectRequestGuest(r)
			if g.Status != want || g.DurationPrecision != "native_retained_may_be_bucketed" {
				t.Fatalf("guest status=%s want=%s", g.Status, want)
			}
			if (want == "unavailable" || want == "invalid_native_guest") && g.DurationMS != nil {
				t.Fatal("unavailable/invalid guest exposed duration")
			}
			if mode == "zero" && (g.DurationMS == nil || *g.DurationMS != 0 || g.ErrorClass != "not_reported") {
				t.Fatal("observed zero or absent error class misrepresented")
			}
			if mode == "aggregate" && !g.RepresentsMultipleRequests {
				t.Fatal("collapsed request count lost")
			}
			encoded, _ := json.Marshal(g)
			if strings.Contains(string(encoded), "excluded-") || strings.Contains(string(encoded), "fp_test_") {
				t.Fatal("unreviewed guest text leaked")
			}
		})
	}
}

func TestRequestGuestCategoriesAndFailureFindings(t *testing.T) {
	for _, test := range []struct{ outcome, class string }{{"ok", ""}, {"http_error", "http_5xx"}, {"handler_error", "handler_exec"}, {"handler_error", "handler_protocol"}, {"timeout", "timeout"}, {"canceled", "canceled"}, {"timeout", ""}} {
		t.Run(test.outcome+"/"+test.class, func(t *testing.T) {
			for _, runtime := range []string{"node22", "node24", "python312", "python313", "go124", "binary"} {
				e := withRequestGuest(requestFixtureEvidence())
				e.Request.Guest.Runtime, e.Request.Guest.Outcome, e.Request.Guest.ErrorClass = runtime, test.outcome, test.class
				// Runner evidence is independent of the edge HTTP outcome.
				e.Request.Status, e.Request.Count = 200, 7
				m := &management{}
				v := m.projectRequestEvidence(m.projectRequest(requestArgs{App: "alpha", RequestID: "row-1"}, "app-1", "default", e.Request), e, nil)
				if v.Request.Guest.Status != "observed" || v.Request.Guest.Runtime != runtime {
					t.Fatal("supported runtime/outcome lost")
				}
				found := false
				for _, f := range v.Explanation.Findings {
					if f.Code == "guest_failure" {
						found = true
						if f.Confidence != "high" || f.EvidenceRefs[0] != "/request/guest" || !f.Approximate {
							t.Fatal("guest confidence, reference or aggregate semantics lost")
						}
					}
				}
				if found != (test.outcome != "ok") {
					t.Fatal("guest failure inferred from HTTP outcome or successful runner")
				}
				if found && (len(v.Explanation.Recommendations) != 1 || v.Explanation.Recommendations[0].EvidenceRefs[0] != "/request/guest") {
					t.Fatal("investigation suggestion did not cite supported guest evidence")
				}
				assertExplanationReferences(t, v)
			}
		})
	}
}

func TestRequestGuestUncertainEvidenceSuppressesFindings(t *testing.T) {
	for _, mode := range []string{"absent", "unknown_runtime", "unknown_outcome", "unknown_class", "missing", "mismatch", "invalid_duration", "wrong_reference", "journal"} {
		t.Run(mode, func(t *testing.T) {
			e := withRequestGuest(requestFixtureEvidence())
			switch mode {
			case "absent":
				e.Request.Guest = nil
			case "unknown_runtime":
				e.Request.Guest.Runtime = "excluded-runtime"
			case "unknown_outcome":
				e.Request.Guest.Outcome = "excluded-outcome"
			case "unknown_class":
				e.Request.Guest.ErrorClass = "excluded-class"
			case "missing":
				e.Request.Guest.Outcome = "missing"
			case "mismatch":
				e.Request.Guest.ErrorClass = "http_5xx"
			case "invalid_duration":
				e.Request.Guest.DurationMS = -1
			case "wrong_reference":
				e.Explanation.Findings[4].EvidenceRefs = []string{"guest:excluded-private-id"}
			case "journal":
				e.Request.EvidenceStatus = "request_id_only"
			}
			m := &management{}
			v := m.projectRequestEvidence(m.projectRequest(requestArgs{App: "alpha", RequestID: "row-1"}, "app-1", "default", e.Request), e, nil)
			for _, f := range v.Explanation.Findings {
				if f.Code == "guest_failure" {
					t.Fatal("uncertain guest evidence became a failure finding")
				}
			}
			assertExplanationReferences(t, v)
		})
	}
}
