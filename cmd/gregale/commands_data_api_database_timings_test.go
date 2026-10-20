package main_test

import (
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/cmd/gregale"
	"strings"
	"testing"
)

func databaseTimingFixture(t *testing.T, id int, duration int, state string, aborted bool) string {
	t.Helper()
	value := fmt.Sprintf(`{"version":1,"source":"postgrest_server_timing","state":"observed","sql_execution":"unknown","pool_wait":"unknown","phases":{"jwt":{"duration_ms":0,"complete":true},"parse":{"duration_ms":0.1,"complete":true},"plan":{"duration_ms":0.2,"complete":true},"transaction":{"duration_ms":%d,"complete":true,"secret":"private-sentinel"},"response":{"duration_ms":0.3,"complete":true}}}`, duration)
	if state != "observed" {
		value = fmt.Sprintf(`{"version":1,"source":"postgrest_server_timing","state":"unknown","reason":%q,"sql_execution":"unknown","pool_wait":"unknown"}`, state)
	}
	line := dataAPIDiagnosticFixture(t, id, "rest", "completed", 200, float64(duration), nil)
	if aborted {
		line = dataAPIDiagnosticFixture(t, id, "rest", "aborted", nil, float64(duration), nil)
	}
	return strings.TrimSuffix(line, "}") + `,"database_timings":` + value + "}"
}

func TestDataAPIDatabaseTimingCoverageAndCompletedEngineSamples(t *testing.T) {
	c := main.NewDataAPIDiagnosticsCollector("", "")
	for i := 1; i <= 100; i++ {
		line := databaseTimingFixture(t, i, i, "observed", i == 100)
		c.Observe(line)
		c.Observe(line)
	}
	c.Observe(databaseTimingFixture(t, 101, 999, "no_response", true))
	c.Observe(databaseTimingFixture(t, 102, 999, "missing_header", false))
	c.Observe(databaseTimingFixture(t, 103, 999, "invalid_header", false))
	c.Observe(dataAPIDiagnosticFixture(t, 104, "rest", "completed", 200, 1000, nil))
	c.Finish()
	s := (*c.Report()).DataRequests.DatabaseTimings
	phase := s.ByPhase["transaction"]
	if (*c.Report()).Partial || (*c.Report()).Duplicates != 100 || s.ObservedRequests != 100 || s.UnknownRequests != 3 || s.WithoutTimings != 1 || phase.Completed != 100 || phase.Interrupted != 0 {
		t.Fatalf("wrong database timing coverage: %+v", s)
	}
	if *phase.LatencyMS.P50 != 50 || *phase.LatencyMS.P95 != 95 || *phase.LatencyMS.P99 != 99 || *s.ByPhase["jwt"].LatencyMS.P50 != 0 || s.SQLExecution != "unknown" || s.PoolWait != "unknown" {
		t.Fatalf("incorrect database timing percentiles or unsupported attribution: %+v", s)
	}
	for _, reason := range []string{"no_response", "missing_header", "invalid_header"} {
		if s.UnknownByReason[reason] != 1 {
			t.Fatalf("missing unknown reason %s", reason)
		}
	}
	if (*c.Report()).Summary.DatabaseTimings.ObservedRequests != 100 || (*c.Report()).ByRoute["rest"].DatabaseTimings.ObservedRequests != 100 {
		t.Fatal("missing database timing aggregates")
	}
	lookup := main.NewDataAPIDiagnosticsCollector("", "00000000-0000-4000-8000-000000000100")
	lookup.Observe(databaseTimingFixture(t, 100, 100, "observed", true))
	lookup.Finish()
	if len((*lookup.Report()).Matches) != 1 || (*lookup.Report()).Matches[0].DatabaseTimings.Phases["transaction"].Complete == nil || !*(*lookup.Report()).Matches[0].DatabaseTimings.Phases["transaction"].Complete {
		t.Fatal("downstream abort must preserve a completed engine measurement in request lookup")
	}
	encoded, err := json.Marshal((*lookup.Report()))
	if err != nil || strings.Contains(string(encoded), "private-sentinel") {
		t.Fatal("untrusted fields escaped the database timing projection")
	}
	stdout, _, restore := main.SwapIO(t)
	defer restore()
	if err := main.WriteDataAPIDatabaseTimings(s); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"100 observed, 3 unknown, 1 without timings", "pool acquisition + SQL", "50.000 / 95.000 / 99.000 ms", "SQL execution: unknown; pool wait: unknown"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("missing %q in %s", want, stdout.String())
		}
	}
}

func TestDataAPIDatabaseTimingRejectsMalformedAndUntrustedAttribution(t *testing.T) {
	valid := databaseTimingFixture(t, 1, 1, "observed", false)
	for _, line := range []string{
		strings.Replace(valid, `"version":1`, `"version":2`, 1),
		strings.Replace(valid, `"postgrest_server_timing"`, `"private-sentinel"`, 1),
		strings.Replace(valid, `"sql_execution":"unknown"`, `"sql_execution":"observed"`, 1),
		strings.Replace(valid, `"pool_wait":"unknown"`, `"pool_wait":"observed"`, 1),
		strings.Replace(valid, `"transaction":`, `"private-sentinel":`, 1),
		strings.Replace(valid, `"duration_ms":0,`, `"duration_ms":-1,`, 1),
		strings.Replace(valid, `"complete":true`, `"complete":false`, 1),
		strings.Replace(valid, `"complete":true`, `"complete":null`, 1),
		strings.Replace(valid, `"duration_ms":0,`, `"duration_ms":null,`, 1),
		strings.Replace(valid, `,"parse":{"duration_ms":0.1,"complete":true}`, ``, 1),
		databaseTimingFixture(t, 1, 1, "private-sentinel", false),
		strings.TrimSuffix(dataAPIDiagnosticFixture(t, 1, "rest", "completed", 200, 1, nil), "}") + `,"database_timings":null}`,
	} {
		c := main.NewDataAPIDiagnosticsCollector("", "")
		c.Observe(line)
		c.Finish()
		if !(*c.Report()).Partial || (*c.Report()).Invalid != 1 || (*c.Report()).Summary.Requests != 0 {
			t.Fatalf("accepted invalid database timing: %s", line)
		}
		body, err := json.Marshal((*c.Report()))
		if err != nil || strings.Contains(string(body), "private-sentinel") {
			t.Fatal("invalid metadata leaked into diagnostics")
		}
	}
}

func TestDataAPIDatabaseTimingLegacyCoverageNeverClaimsZeroLatency(t *testing.T) {
	c := main.NewDataAPIDiagnosticsCollector("", "")
	c.Observe(dataAPIDiagnosticFixture(t, 1, "rest", "completed", 200, 1, nil))
	c.Finish()
	s := (*c.Report()).DataRequests.DatabaseTimings
	if (*c.Report()).Partial || s.WithoutTimings != 1 || s.ObservedRequests != 0 || s.UnknownRequests != 0 || len(s.ByPhase) != 0 {
		t.Fatalf("legacy database timing coverage: %+v", s)
	}
	stdout, _, restore := main.SwapIO(t)
	defer restore()
	if err := main.WriteDataAPIDatabaseTimings(s); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "completed p50/p95/p99: n/a") {
		t.Fatal("missing engine samples were presented as zero latency")
	}
}
