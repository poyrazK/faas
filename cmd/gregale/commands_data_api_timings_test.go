// adr: 650 — phase coverage and interrupted work must remain explicit.
package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func timingDiagnosticFixture(t *testing.T, id int, timings string, interrupted bool) string {
	t.Helper()
	outcome, status := "completed", any(200)
	if interrupted {
		outcome, status = "aborted", nil
	}
	line := dataAPIDiagnosticFixture(t, id, "rest", outcome, status, 100, nil)
	return strings.TrimSuffix(line, "}") + `,"timings":` + timings + "}"
}

func TestDataAPITimingDiagnosticsCoveragePercentilesAndLookup(t *testing.T) {
	c := newDataAPIDiagnosticsCollector("", "")
	for i := 1; i <= 100; i++ {
		line := timingDiagnosticFixture(t, i, fmt.Sprintf(`{"verification":{"duration_ms":0,"complete":true},"upstream":{"duration_ms":%d,"complete":true,"secret":"private-sentinel"}}`, i), false)
		c.observe(line)
		c.observe(line)
	}
	c.observe(timingDiagnosticFixture(t, 101, `{"verification":{"duration_ms":1,"complete":true},"upstream":{"duration_ms":999,"complete":false}}`, true))
	c.observe(dataAPIDiagnosticFixture(t, 102, "rest", "completed", 200, 10, nil))
	c.finish()
	s := c.report.Summary.Timings
	phase := s.ByPhase["upstream"]
	if c.report.Partial || c.report.Duplicates != 100 || s.ObservedRequests != 101 || s.WithoutTimings != 1 || phase.Observed != 101 || phase.Completed != 100 || phase.Interrupted != 1 {
		t.Fatalf("wrong timing coverage: %+v; phase=%+v", s, phase)
	}
	if *phase.LatencyMS.P50 != 50 || *phase.LatencyMS.P95 != 95 || *phase.LatencyMS.P99 != 99 || *phase.InterruptedElapsedMS.P95 != 999 || *s.ByPhase["verification"].LatencyMS.P50 != 0 {
		t.Fatalf("interrupted elapsed time contaminated completed percentiles: %+v", phase)
	}
	if c.report.DataRequests.Timings.ObservedRequests != 101 || c.report.ByRoute["rest"].Timings.ObservedRequests != 101 {
		t.Fatal("timings missing from route/data aggregates")
	}
	lookup := newDataAPIDiagnosticsCollector("", "00000000-0000-4000-8000-000000000101")
	lookup.observe(timingDiagnosticFixture(t, 101, `{"upstream":{"duration_ms":999,"complete":false,"secret":"private-sentinel"}}`, true))
	lookup.observe(dataAPIDiagnosticFixture(t, 102, "rest", "completed", 200, 10, nil))
	lookup.finish()
	if lookup.report.Filtered != 1 || len(lookup.report.Matches) != 1 || lookup.report.Summary.Timings.ByPhase["upstream"].LatencyMS.P95 != nil {
		t.Fatal("lookup or missing completed-sample coverage is incorrect")
	}
	encoded, err := json.Marshal(lookup.report)
	if err != nil || strings.Contains(string(encoded), "private-sentinel") {
		t.Fatal("timing report exported an untrusted field")
	}
	stdout, _, restore := swapIO(t)
	defer restore()
	if err := writeDataAPITimings(lookup.report.Summary.Timings); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "0 completed, 1 interrupted; completed p50/p95/p99: n/a") {
		t.Fatalf("human output hides missing completed samples: %s", stdout.String())
	}
}

func TestDataAPITimingDiagnosticsRejectMalformedMetadata(t *testing.T) {
	for _, timing := range []string{
		`{}`, `[]`, `{"private-sentinel":{"duration_ms":1,"complete":true}}`,
		`{"upstream":null}`, `{"upstream":{"complete":true}}`,
		`{"upstream":{"duration_ms":0}}`, `{"upstream":{"duration_ms":-1,"complete":true}}`,
		`{"upstream":{"duration_ms":"private-sentinel","complete":true}}`,
		`{"upstream":{"duration_ms":1,"complete":null}}`, `{"upstream":{"duration_ms":1,"complete":"true"}}`,
	} {
		c := newDataAPIDiagnosticsCollector("", "")
		c.observe(timingDiagnosticFixture(t, 1, timing, false))
		c.finish()
		if !c.report.Partial || c.report.Invalid != 1 || c.report.Summary.Requests != 0 {
			t.Fatalf("accepted malformed timings %s", timing)
		}
	}
	c := newDataAPIDiagnosticsCollector("", "")
	c.observe(dataAPIDiagnosticFixture(t, 1, "rest", "completed", 200, 1, nil))
	c.finish()
	if c.report.Partial || c.report.Summary.Timings.ObservedRequests != 0 || c.report.Summary.Timings.WithoutTimings != 1 || len(c.report.Summary.Timings.ByPhase) != 0 {
		t.Fatal("legacy timing coverage must remain explicit")
	}
}

func TestDataAPIQueryShapeRanking(t *testing.T) {
	p95 := func(value float64) dataAPILatency { return dataAPILatency{P95: &value} }
	shapes := []dataAPIShapeStats{
		{Operation: "api.frequent", Stats: dataAPIRequestStats{Requests: 10, Errors: 1, LatencyMS: p95(10)}},
		{Operation: "api.slow", Stats: dataAPIRequestStats{Requests: 2, Errors: 0, LatencyMS: p95(100)}},
		{Operation: "api.failed", Stats: dataAPIRequestStats{Requests: 4, Errors: 3, LatencyMS: p95(20)}},
		{Operation: "api.unknown", Stats: dataAPIRequestStats{Requests: 0}},
	}
	for order, want := range map[string][]string{
		"requests": {"api.frequent", "api.failed", "api.slow", "api.unknown"},
		"p95":      {"api.slow", "api.failed", "api.frequent", "api.unknown"},
		"errors":   {"api.failed", "api.frequent", "api.slow", "api.unknown"},
	} {
		got := slices.Clone(shapes)
		rankDataAPIQueryShapes(got, order)
		for i := range got {
			if got[i].Operation != want[i] {
				t.Fatalf("%s: incorrect rank %d: %s", order, i, got[i].Operation)
			}
		}
	}
	tied := []dataAPIShapeStats{{Operation: "api.z"}, {Operation: "api.a"}}
	rankDataAPIQueryShapes(tied, "p95")
	if tied[0].Operation != "api.a" {
		t.Fatal("missing timings must sort deterministically")
	}
}
