// adr: 650 — phase coverage and interrupted work must remain explicit.
package main_test

import (
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/cmd/gregale"
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
	c := main.NewDataAPIDiagnosticsCollector("", "")
	for i := 1; i <= 100; i++ {
		line := timingDiagnosticFixture(t, i, fmt.Sprintf(`{"verification":{"duration_ms":0,"complete":true},"upstream":{"duration_ms":%d,"complete":true,"secret":"private-sentinel"}}`, i), false)
		c.Observe(line)
		c.Observe(line)
	}
	c.Observe(timingDiagnosticFixture(t, 101, `{"verification":{"duration_ms":1,"complete":true},"upstream":{"duration_ms":999,"complete":false}}`, true))
	c.Observe(dataAPIDiagnosticFixture(t, 102, "rest", "completed", 200, 10, nil))
	c.Finish()
	s := (*c.Report()).Summary.Timings
	phase := s.ByPhase["upstream"]
	if (*c.Report()).Partial || (*c.Report()).Duplicates != 100 || s.ObservedRequests != 101 || s.WithoutTimings != 1 || phase.Observed != 101 || phase.Completed != 100 || phase.Interrupted != 1 {
		t.Fatalf("wrong timing coverage: %+v; phase=%+v", s, phase)
	}
	if *phase.LatencyMS.P50 != 50 || *phase.LatencyMS.P95 != 95 || *phase.LatencyMS.P99 != 99 || *phase.InterruptedElapsedMS.P95 != 999 || *s.ByPhase["verification"].LatencyMS.P50 != 0 {
		t.Fatalf("interrupted elapsed time contaminated completed percentiles: %+v", phase)
	}
	if (*c.Report()).DataRequests.Timings.ObservedRequests != 101 || (*c.Report()).ByRoute["rest"].Timings.ObservedRequests != 101 {
		t.Fatal("timings missing from route/data aggregates")
	}
	lookup := main.NewDataAPIDiagnosticsCollector("", "00000000-0000-4000-8000-000000000101")
	lookup.Observe(timingDiagnosticFixture(t, 101, `{"upstream":{"duration_ms":999,"complete":false,"secret":"private-sentinel"}}`, true))
	lookup.Observe(dataAPIDiagnosticFixture(t, 102, "rest", "completed", 200, 10, nil))
	lookup.Finish()
	if (*lookup.Report()).Filtered != 1 || len((*lookup.Report()).Matches) != 1 || (*lookup.Report()).Summary.Timings.ByPhase["upstream"].LatencyMS.P95 != nil {
		t.Fatal("lookup or missing completed-sample coverage is incorrect")
	}
	encoded, err := json.Marshal((*lookup.Report()))
	if err != nil || strings.Contains(string(encoded), "private-sentinel") {
		t.Fatal("timing report exported an untrusted field")
	}
	stdout, _, restore := main.SwapIO(t)
	defer restore()
	if err := main.WriteDataAPITimings((*lookup.Report()).Summary.Timings); err != nil {
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
		c := main.NewDataAPIDiagnosticsCollector("", "")
		c.Observe(timingDiagnosticFixture(t, 1, timing, false))
		c.Finish()
		if !(*c.Report()).Partial || (*c.Report()).Invalid != 1 || (*c.Report()).Summary.Requests != 0 {
			t.Fatalf("accepted malformed timings %s", timing)
		}
	}
	c := main.NewDataAPIDiagnosticsCollector("", "")
	c.Observe(dataAPIDiagnosticFixture(t, 1, "rest", "completed", 200, 1, nil))
	c.Finish()
	if (*c.Report()).Partial || (*c.Report()).Summary.Timings.ObservedRequests != 0 || (*c.Report()).Summary.Timings.WithoutTimings != 1 || len((*c.Report()).Summary.Timings.ByPhase) != 0 {
		t.Fatal("legacy timing coverage must remain explicit")
	}
}

func TestDataAPIQueryShapeRanking(t *testing.T) {
	p95 := func(value float64) main.DataAPILatency { return main.DataAPILatency{P95: &value} }
	shapes := []main.DataAPIShapeStats{
		{Operation: "api.frequent", Stats: main.DataAPIRequestStats{Requests: 10, Errors: 1, LatencyMS: p95(10)}},
		{Operation: "api.slow", Stats: main.DataAPIRequestStats{Requests: 2, Errors: 0, LatencyMS: p95(100)}},
		{Operation: "api.failed", Stats: main.DataAPIRequestStats{Requests: 4, Errors: 3, LatencyMS: p95(20)}},
		{Operation: "api.unknown", Stats: main.DataAPIRequestStats{Requests: 0}},
	}
	for order, want := range map[string][]string{
		"requests": {"api.frequent", "api.failed", "api.slow", "api.unknown"},
		"p95":      {"api.slow", "api.failed", "api.frequent", "api.unknown"},
		"errors":   {"api.failed", "api.frequent", "api.slow", "api.unknown"},
	} {
		got := slices.Clone(shapes)
		main.RankDataAPIQueryShapes(got, order)
		for i := range got {
			if got[i].Operation != want[i] {
				t.Fatalf("%s: incorrect rank %d: %s", order, i, got[i].Operation)
			}
		}
	}
	tied := []main.DataAPIShapeStats{{Operation: "api.z"}, {Operation: "api.a"}}
	main.RankDataAPIQueryShapes(tied, "p95")
	if tied[0].Operation != "api.a" {
		t.Fatal("missing timings must sort deterministically")
	}
}
