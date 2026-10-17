package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func dataAPIDiagnosticFixture(t *testing.T, id int, route, outcome string, status any, duration float64, code any) string {
	t.Helper()
	content, err := json.Marshal(map[string]any{"time": "2026-10-11T10:00:00Z", "level": "info", "event": "data_api_request", "request_id": fmt.Sprintf("00000000-0000-4000-8000-%012d", id), "method": "GET", "route": route, "status": status, "duration_ms": duration, "outcome": outcome, "code": code})
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
func dataAPIDiagnosticEnvelope(t *testing.T, line string) string {
	t.Helper()
	content, err := json.Marshal(api.LogEvent{Seq: 1, InstanceID: "instance", Stream: "stderr", Line: line, WrittenAt: "2026-10-11T10:00:01Z"})
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
func TestDataAPIDiagnosticsAggregationAndLookup(t *testing.T) {
	c := newDataAPIDiagnosticsCollector("", "")
	lines := []string{
		dataAPIDiagnosticFixture(t, 1, "rest", "completed", 200, 1, nil),
		dataAPIDiagnosticFixture(t, 2, "rest", "authentication_failed", 401, 2, "token_invalid"),
		dataAPIDiagnosticFixture(t, 3, "rest", "query_timeout", 504, 30, "query_timeout"),
		dataAPIDiagnosticFixture(t, 4, "rpc", "unavailable", 503, 40, "data_api_unavailable"),
		dataAPIDiagnosticFixture(t, 5, "rpc", "rejected", 409, 50, "schema_contract_mismatch"),
		dataAPIDiagnosticFixture(t, 6, "rpc", "upstream_error", 500, 60, "upstream_error"),
		dataAPIDiagnosticFixture(t, 7, "rest", "aborted", nil, 70, nil),
		dataAPIDiagnosticFixture(t, 8, "health", "completed", 200, 100, nil),
		dataAPIDiagnosticFixture(t, 9, "schema", "completed", 200, 200, nil),
		dataAPIDiagnosticFixture(t, 10, "openapi", "completed", 200, 300, nil),
		dataAPIDiagnosticFixture(t, 11, "unknown", "rejected", 404, 400, "not_found"),
	}
	for _, line := range lines {
		if !c.observe(dataAPIDiagnosticEnvelope(t, line)) {
			t.Fatal("unexpected cap")
		}
	}
	c.observe(lines[0])
	c.observe("a plain log with private-body must be ignored")
	c.finish()
	if c.report.Summary.Requests != 11 || c.report.Summary.Errors != 7 || c.report.Summary.HTTPErrors != 6 || c.report.Summary.ServerErrors != 3 || c.report.Summary.Aborted != 1 || c.report.DataRequests.Requests != 7 || c.report.Authentication.Requests != 1 || c.report.Duplicates != 1 || c.report.Ignored != 1 || c.report.Partial {
		t.Fatalf("incorrect summary: %+v", c.report)
	}
	if *c.report.Summary.LatencyMS.P50 != 60 || *c.report.Summary.LatencyMS.P95 != 400 || *c.report.Summary.LatencyMS.P99 != 400 || *c.report.DataRequests.LatencyMS.P50 != 40 {
		t.Fatalf("incorrect percentiles: %+v", c.report.Summary.LatencyMS)
	}
	if math.Abs(*c.report.Summary.ErrorRate-700.0/11) > 1e-9 || c.report.Failures != (dataAPIFailureCounts{Authentication: 1, Timeouts: 1, Unavailable: 1, ContractMismatch: 1, Upstream: 1}) {
		t.Fatal("incorrect failure counts")
	}
	lookup := newDataAPIDiagnosticsCollector("2026-10-11T09:00:00Z", "00000000-0000-4000-8000-000000000002")
	for _, line := range lines {
		lookup.observe(line)
	}
	lookup.observe(lines[1])
	lookup.finish()
	if lookup.report.Summary.Requests != 1 || len(lookup.report.Matches) != 1 || lookup.report.Filtered != 10 || lookup.report.Duplicates != 1 || lookup.report.Matches[0].Code == nil || *lookup.report.Matches[0].Code != "token_invalid" {
		t.Fatalf("lookup: %+v", lookup.report)
	}
	empty := newDataAPIDiagnosticsCollector("2026-10-11T11:00:00Z", "")
	empty.observe(lines[0])
	empty.finish()
	if empty.report.Summary.ErrorRate != nil || empty.report.Summary.LatencyMS.P50 != nil || empty.report.Filtered != 1 {
		t.Fatal("empty sample must not claim zero latency or error rate")
	}
	// nearest-rank percentiles, including exact p95 for a known larger sample.
	many := newDataAPIDiagnosticsCollector("", "")
	for i := 1; i <= 100; i++ {
		many.observe(dataAPIDiagnosticFixture(t, i, "rest", "completed", 200, float64(i), nil))
	}
	many.finish()
	if *many.report.Summary.LatencyMS.P50 != 50 || *many.report.Summary.LatencyMS.P95 != 95 || *many.report.Summary.LatencyMS.P99 != 99 {
		t.Fatal("nearest-rank percentiles incorrect")
	}
}

func TestDataAPIDiagnosticsNeverExportsUntrustedLogData(t *testing.T) {
	valid := dataAPIDiagnosticFixture(t, 1, "rest", "completed", 200, 1, nil)
	for _, field := range []string{"route", "method", "outcome", "code", "request_id", "time"} {
		var record map[string]any
		if err := json.Unmarshal([]byte(valid), &record); err != nil {
			t.Fatal(err)
		}
		record[field] = "private-jwt-and-sql-secret"
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		c := newDataAPIDiagnosticsCollector("", "")
		c.observe(string(raw))
		c.finish()
		if !c.report.Partial || c.report.Invalid != 1 || c.report.Summary.Requests != 0 {
			t.Fatalf("accepted unsafe %s", field)
		}
		report, err := json.Marshal(c.report)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(report, []byte("private-jwt")) {
			t.Fatal("untrusted value exported")
		}
	}
	for _, mutation := range []func(map[string]any){
		func(m map[string]any) { delete(m, "status") }, func(m map[string]any) { delete(m, "duration_ms") },
		func(m map[string]any) { m["status"] = 700 }, func(m map[string]any) { m["status"] = nil },
		func(m map[string]any) { m["duration_ms"] = -1 }, func(m map[string]any) { m["duration_ms"] = "private" },
	} {
		var record map[string]any
		if err := json.Unmarshal([]byte(valid), &record); err != nil {
			t.Fatal(err)
		}
		mutation(record)
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if _, candidate, err := parseDataAPIRequestRecord(string(raw)); !candidate || err == nil {
			t.Fatal("accepted malformed metadata")
		}
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(valid), &record); err != nil {
		t.Fatal(err)
	}
	record["authorization"] = "private-jwt"
	record["sql"] = "private-sql"
	record["body"] = "private-body"
	record["path"] = "/private/table?secret=private-query"
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	c := newDataAPIDiagnosticsCollector("", "00000000-0000-4000-8000-000000000001")
	c.observe(string(raw))
	c.finish()
	report, err := json.Marshal(c.report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(report, []byte("private")) || c.report.Summary.Requests != 1 {
		t.Fatalf("unknown fields leaked %s", report)
	}
}

func TestDataAPIDiagnosticsStreamCompletenessAndBounds(t *testing.T) {
	line := dataAPIDiagnosticEnvelope(t, dataAPIDiagnosticFixture(t, 1, "rest", "completed", 200, 10, nil))
	logFrame := "event: log\ndata: " + line + "\n\n"
	for _, scenario := range []struct {
		name, content, reason string
		requests              int
	}{
		{"clean", ": heartbeat\r\n\r\n" + logFrame + "event: end\ndata: {}\n\n", "", 1},
		{"terminal-at-eof", logFrame + "event: end\ndata: {}", "", 1},
		{"gap", logFrame + "event: gap\ndata: {\"reason\":\"private-secret\"}\n\nevent: end\ndata: {}\n\n", "log_gap", 1},
		{"degraded", logFrame + "event: degraded\ndata: {\"detail\":\"private-secret\"}\n\n", "stream_unavailable", 1},
		{"error", logFrame + "event: error\ndata: {\"message\":\"private-secret\"}\n\n", "stream_unavailable", 1},
		{"end-error", logFrame + "event: end\ndata: {\"reason\":\"private-secret\"}\n\n", "stream_end_error", 1},
		{"missing-end", logFrame, "missing_end", 1},
		{"unknown-frame", logFrame + "event: private-secret\ndata: {}\n\nevent: end\ndata: {}\n\n", "unsupported_frame", 1},
		{"bad-record", "event: log\ndata: {\"line\":\"{ data_api_request private-secret\"}\n\nevent: end\ndata: {}\n\n", "invalid_records", 0},
		{"multi-line", strings.Replace(logFrame, "data: {", "data: {\ndata: ", 1) + "event: end\ndata: {}\n\n", "", 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			c := newDataAPIDiagnosticsCollector("", "")
			readDataAPIDiagnostics(context.Background(), strings.NewReader(scenario.content), true, c)
			c.finish()
			if c.report.Partial != (scenario.reason != "") || c.report.Summary.Requests != scenario.requests {
				t.Fatalf("incorrect report %+v", c.report)
			}
			if scenario.reason != "" && !dataAPIDiagnosticAllowed(scenario.reason, c.report.PartialReasons...) {
				t.Fatalf("missing reason %+v", c.report)
			}
			report, err := json.Marshal(c.report)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(report, []byte("private-secret")) {
				t.Fatal("stream metadata leaked")
			}
		})
	}
	c := newDataAPIDiagnosticsCollector("", "")
	c.report.InputRecords = api.DataAPIDiagnosticsMaxRecords
	readDataAPIDiagnostics(context.Background(), strings.NewReader(line+"\n"), false, c)
	if !dataAPIDiagnosticAllowed("record_limit", c.report.PartialReasons...) {
		t.Fatal("record limit not enforced")
	}
	c = newDataAPIDiagnosticsCollector("", "")
	readDataAPIDiagnostics(context.Background(), strings.NewReader(strings.Repeat("x", api.LogRingBufferBytes+1)), false, c)
	if !dataAPIDiagnosticAllowed("read_error", c.report.PartialReasons...) {
		t.Fatal("line limit not enforced")
	}
	c = newDataAPIDiagnosticsCollector("", "")
	readDataAPIDiagnostics(context.Background(), strings.NewReader("event: log\n"+strings.Repeat("data: "+strings.Repeat("x", 1000)+"\n", 70)+"\n"), true, c)
	if !dataAPIDiagnosticAllowed("frame_limit", c.report.PartialReasons...) {
		t.Fatal("frame limit not enforced")
	}
	c = newDataAPIDiagnosticsCollector("", "")
	readDataAPIDiagnostics(context.Background(), strings.NewReader(strings.Repeat(": "+strings.Repeat("x", 1000)+"\n", api.DataAPIDiagnosticsMaxBytes/1000+10)), true, c)
	if !dataAPIDiagnosticAllowed("byte_limit", c.report.PartialReasons...) {
		t.Fatal("byte limit not enforced")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c = newDataAPIDiagnosticsCollector("", "")
	readDataAPIDiagnostics(ctx, strings.NewReader(logFrame), true, c)
	if !dataAPIDiagnosticAllowed("cancelled", c.report.PartialReasons...) {
		t.Fatal("cancellation not reported")
	}
}

type dataAPIDiagnosticsBrokenReader struct {
	first   bool
	content string
}

func (r *dataAPIDiagnosticsBrokenReader) Read(b []byte) (int, error) {
	if !r.first {
		r.first = true
		return copy(b, r.content), nil
	}
	return 0, fmt.Errorf("private-secret")
}
func TestDataAPIDiagnosticsTransportFailureDoesNotBecomeCleanEOF(t *testing.T) {
	c := newDataAPIDiagnosticsCollector("", "")
	readDataAPIDiagnostics(context.Background(), &dataAPIDiagnosticsBrokenReader{content: "event: end\ndata: {}"}, true, c)
	c.finish()
	if !c.report.Partial || !dataAPIDiagnosticAllowed("read_error", c.report.PartialReasons...) {
		t.Fatalf("transport error looked complete %+v", c.report)
	}
}

func TestDataAPIDiagnosticsCLI(t *testing.T) {
	for _, scenario := range []string{"hosted", "file", "lookup", "no-match", "human", "gap", "deadline", "invalid-id", "bad-since", "ambiguous", "offline-deployment", "sort-p95", "sort-errors", "invalid-sort"} {
		t.Run(scenario, func(t *testing.T) {
			line := dataAPIDiagnosticFixture(t, 1, "rest", "query_timeout", 504, 15000, "query_timeout")
			line = strings.Replace(line, "}", `,"authorization":"owner-secret","body":"private-body","sql":"private-sql"}`, 1)
			envelope := dataAPIDiagnosticEnvelope(t, line)
			var calls atomic.Int32
			management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || r.URL.Path != "/v1/apps/notes/logs" || r.URL.Query().Get("follow") != "0" || r.URL.Query().Get("grep") != "" || r.URL.Query().Get("level") != "" {
					t.Errorf("unexpected query %s", r.URL)
				}
				if r.URL.Query().Get("deployment") != "revision" || r.URL.Query().Get("since") != "2026-10-11T09:00:00Z" {
					t.Errorf("missing filters %s", r.URL)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if scenario == "deadline" {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", envelope)
				if scenario == "gap" {
					_, _ = fmt.Fprint(w, "event: gap\ndata: {\"reason\":\"private-secret\"}\n\n")
				}
				_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
			}))
			defer management.Close()
			t.Setenv("FAAS_API", management.URL)
			t.Setenv("FAAS_TOKEN", "owner-secret")
			stdout, stderr, restore := swapIO(t)
			defer restore()
			args := []string{"--json", "data-api", "diagnostics", "notes", "--deployment", "revision", "--since", "2026-10-11T09:00:00Z"}
			offline := scenario == "file" || scenario == "lookup" || scenario == "no-match" || scenario == "human" || scenario == "offline-deployment"
			file := filepath.Join(t.TempDir(), "logs.ndjson")
			if offline || scenario == "ambiguous" {
				if err := os.WriteFile(file, []byte(envelope+"\n"+line+"\n"+"plain private-secret\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if offline {
				t.Setenv("FAAS_TOKEN", "")
				args = []string{"--json", "data-api", "diagnostics", "--file", file, "--since", "2026-10-11T09:00:00Z"}
			}
			switch scenario {
			case "sort-p95":
				args = append(args, "--sort", "p95")
			case "sort-errors":
				args = append(args, "--sort", "errors")
			case "invalid-sort":
				args = append(args, "--sort", "private-secret")
			case "lookup":
				args = append(args, "--request-id", "00000000-0000-4000-8000-000000000001")
			case "no-match":
				args = append(args, "--request-id", "00000000-0000-4000-8000-000000000099")
			case "human":
				args = args[1:]
			case "gap":
			case "deadline":
				args = append(args, "--timeout", "200ms")
			case "invalid-id":
				args = append(args, "--request-id", "private-secret")
			case "bad-since":
				args = append(args, "--since", "private-secret")
			case "ambiguous":
				args = append(args, "--file", file)
			case "offline-deployment":
				args = append(args, "--deployment", "revision")
			}
			code := run(args)
			invalid := scenario == "invalid-id" || scenario == "bad-since" || scenario == "ambiguous" || scenario == "offline-deployment" || scenario == "invalid-sort"
			if (code != 0) != (invalid || scenario == "gap" || scenario == "deadline") {
				t.Fatalf("exit=%d %s err=%s", code, stdout.String(), stderr())
			}
			if offline || invalid {
				if calls.Load() != 0 {
					t.Fatal("offline or invalid input contacted API")
				}
			} else if calls.Load() != 1 {
				t.Fatalf("calls %d", calls.Load())
			}
			if !invalid && scenario != "human" {
				var report dataAPIDiagnosticsReport
				if err := json.Unmarshal([]byte(stdout.String()), &report); err != nil {
					t.Fatal(err)
				}
				expected := 1
				if scenario == "no-match" || scenario == "deadline" {
					expected = 0
				}
				if report.Summary.Requests != expected || report.Version != 1 {
					t.Fatalf("report %+v", report)
				}
				wantSort := "requests"
				if strings.HasPrefix(scenario, "sort-") {
					wantSort = strings.TrimPrefix(scenario, "sort-")
				}
				if report.QueryShapeSort != wantSort {
					t.Fatalf("wrong shape ranking: %q", report.QueryShapeSort)
				}
				if report.Partial != (scenario == "gap" || scenario == "deadline") {
					t.Fatal("incorrect completeness")
				}
				if offline && report.Coverage != "provided_file" || !offline && report.Coverage != "retained_sample" {
					t.Fatal("incorrect source coverage")
				}
				if scenario == "lookup" && (len(report.Matches) != 1 || report.Duplicates != 1) {
					t.Fatal("request lookup failed")
				}
			}
			if scenario == "human" && !strings.Contains(stdout.String(), "p50/p95/p99 15000.000") {
				t.Fatal("human summary missing latency")
			}
			for _, secret := range []string{"owner-secret", "private-body", "private-sql", "private-secret"} {
				if strings.Contains(stdout.String()+stderr(), secret) {
					t.Fatalf("leaked %s", secret)
				}
			}
			if offline {
				actual, err := os.ReadFile(file)
				if err != nil || !strings.HasPrefix(string(actual), envelope) {
					t.Fatal("offline logs changed")
				}
			}
		})
	}
}

func TestDataAPIDiagnosticsNoLogsIsNotHealthyZero(t *testing.T) {
	c := newDataAPIDiagnosticsCollector("", "")
	readDataAPIDiagnostics(context.Background(), strings.NewReader(""), false, c)
	c.finish()
	output, err := json.Marshal(c.report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output, []byte(`"error_rate_percent":null`)) || !bytes.Contains(output, []byte(`"p95":null`)) {
		t.Fatalf("empty sample claims health: %s", output)
	}
}

func TestDataAPIDiagnosticsSavedGapsAndTimeOrdering(t *testing.T) {
	c := newDataAPIDiagnosticsCollector("2026-10-11T09:00:00Z", "")
	late := strings.Replace(dataAPIDiagnosticFixture(t, 1, "rest", "completed", 200, 1, nil), "10:00:00Z", "12:00:00+02:00", 1)
	early := strings.Replace(dataAPIDiagnosticFixture(t, 2, "rest", "completed", 200, 2, nil), "10:00:00Z", "09:00:00Z", 1)
	c.observe(late)
	c.observe(early)
	c.observe(`{"reason":"since_below_retained","gap_to_written_at":"private-secret","replay_advised":true}`)
	c.finish()
	if c.report.First != "2026-10-11T09:00:00Z" || c.report.Last != "2026-10-11T10:00:00Z" || c.report.Summary.Requests != 2 || c.report.Gaps != 1 || !c.report.Partial {
		t.Fatalf("ordering or gap incorrect %+v", c.report)
	}
}

func TestDataAPIDiagnosticsHelpAndOfflineInputProtection(t *testing.T) {
	stdout, _, restore := swapIO(t)
	defer restore()
	if code := run([]string{"data-api", "diagnostics", "--help"}); code != 0 || !strings.Contains(stdout.String(), "--request-id") || !strings.Contains(stdout.String(), "--file") {
		t.Fatalf("help exit=%d %s", code, stdout.String())
	}
	root := t.TempDir()
	target := filepath.Join(root, "data.ndjson")
	alias := filepath.Join(root, "alias.ndjson")
	if err := os.WriteFile(target, []byte("private-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	for _, file := range []string{alias, root, filepath.Join(root, "missing.ndjson")} {
		if code := run([]string{"--json", "data-api", "diagnostics", "--file", file}); code == 0 {
			t.Fatalf("accepted invalid file %s", file)
		}
	}
	if content, err := os.ReadFile(target); err != nil || string(content) != "private-secret\n" {
		t.Fatal("offline input changed")
	}
	if strings.Contains(stdout.String(), "private-secret") {
		t.Fatal("invalid input printed file content")
	}
}

func TestDataAPIDiagnosticsRefreshAdmission(t *testing.T) {
	c := newDataAPIDiagnosticsCollector("", "")
	c.observe(dataAPIDiagnosticFixture(t, 1, "rest", "rejected", 503, 1, "data_api_refresh_paused"))
	c.observe(dataAPIDiagnosticFixture(t, 2, "rest", "unavailable", 503, 2, "data_api_refresh_unavailable"))
	c.finish()
	if c.report.Partial || c.report.Invalid != 0 || c.report.Summary.Requests != 2 || c.report.Failures.Unavailable != 2 || c.report.Failures.RefreshPaused != 1 || c.report.Failures.RefreshUnavailable != 1 {
		t.Fatalf("refresh admission diagnostics lost: %+v", c.report)
	}
}
