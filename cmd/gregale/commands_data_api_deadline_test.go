package main_test

import (
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/cmd/gregale"
	"strings"
	"testing"
)

func deadlineDiagnosticFixture(t *testing.T, id int, deadline string, streamed bool) string {
	t.Helper()
	outcome, status := "query_timeout", 504
	if streamed {
		outcome, status = "aborted", 200
	}
	line := dataAPIDiagnosticFixture(t, id, "rest", outcome, status, 15000, "request_deadline_exceeded")
	return strings.TrimSuffix(line, "}") + ",\"deadline\":" + deadline + "}"
}

func TestDataAPIDeadlineDiagnostics(t *testing.T) {
	c := main.NewDataAPIDiagnosticsCollector("", "")
	for i, phase := range []string{"verification", "refresh", "readiness", "signing", "upstream", "streaming"} {
		dispatched := phase == "upstream" || phase == "streaming"
		line := deadlineDiagnosticFixture(t, i+1, fmt.Sprintf(`{"phase":%q,"elapsed_ms":15000,"dispatched":%t,"secret":"private-sentinel"}`, phase, dispatched), phase == "streaming")
		c.Observe(line)
		c.Observe(line)
	}
	// Older query timeouts still count, with explicitly missing phase coverage.
	c.Observe(dataAPIDiagnosticFixture(t, 7, "rest", "query_timeout", 504, 15000, "query_timeout"))
	c.Finish()
	if (*c.Report()).Partial || (*c.Report()).Deadlines.Observed != 6 || (*c.Report()).Deadlines.Dispatched != 2 || len((*c.Report()).Deadlines.ByPhase) != 6 || (*c.Report()).Failures.Timeouts != 7 || (*c.Report()).Duplicates != 6 || (*c.Report()).Summary.Aborted != 1 {
		t.Fatalf("invalid deadline aggregation: %+v", (*c.Report()))
	}
	for _, count := range (*c.Report()).Deadlines.ByPhase {
		if count != 1 {
			t.Fatal("deadline phase duplicated")
		}
	}
	encoded, err := json.Marshal((*c.Report()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-sentinel") {
		t.Fatal("unknown nested data leaked")
	}
}

func TestDataAPIDeadlineRejectsUnsafeOrIncompleteRecords(t *testing.T) {
	for _, d := range []string{
		`{}`, `{"phase":"private-sentinel","elapsed_ms":15000,"dispatched":false}`,
		`{"phase":"verification","dispatched":false}`,
		`{"phase":"verification","elapsed_ms":-1,"dispatched":false}`,
		`{"phase":"verification","elapsed_ms":15000,"dispatched":null}`,
		`{"phase":"verification","elapsed_ms":15000,"dispatched":true}`,
		`{"phase":"streaming","elapsed_ms":15000,"dispatched":false}`,
		`{"phase":"upstream","elapsed_ms":"private-sentinel","dispatched":true}`,
	} {
		c := main.NewDataAPIDiagnosticsCollector("", "")
		c.Observe(deadlineDiagnosticFixture(t, 1, d, false))
		if !(*c.Report()).Partial || (*c.Report()).Invalid != 1 || (*c.Report()).Summary.Requests != 0 {
			t.Fatal("malformed deadline metadata was accepted")
		}
	}
	line := deadlineDiagnosticFixture(t, 1, `{"phase":"verification","elapsed_ms":15000,"dispatched":false}`, false)
	for _, mutation := range []string{strings.ReplaceAll(line, "request_deadline_exceeded", "token_invalid"), strings.ReplaceAll(line, "query_timeout", "completed")} {
		if _, _, err := main.ParseDataAPIRequestRecord(mutation); err == nil {
			t.Fatal("inconsistent deadline event accepted")
		}
	}
}

func TestDataAPIDeadlineBeforeDispatchRemainsObservable(t *testing.T) {
	line := deadlineDiagnosticFixture(t, 1, `{"phase":"upstream","elapsed_ms":15000,"dispatched":false}`, false)
	line = strings.TrimSuffix(line, "}") + `,"timings":{"signing":{"duration_ms":14999,"complete":true},"upstream":{"duration_ms":1,"complete":false}}}`
	c := main.NewDataAPIDiagnosticsCollector("", "")
	c.Observe(line)
	c.Finish()
	if (*c.Report()).Partial || (*c.Report()).Summary.Requests != 1 || (*c.Report()).Deadlines.Observed != 1 || (*c.Report()).Deadlines.Dispatched != 0 || (*c.Report()).Summary.Timings.ByPhase["upstream"].Interrupted != 1 {
		t.Fatalf("pre-dispatch deadline was lost or claimed engine execution: %+v", (*c.Report()))
	}
}
