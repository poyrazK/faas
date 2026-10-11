package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type recoveryDiagnosticFake struct {
	payload string
	err     error
	calls   int
	follow  bool
	since   string
}

func (f *recoveryDiagnosticFake) StreamAppLogs(_ context.Context, _ string, _ string, follow bool, filter api.LogFilter) (io.ReadCloser, error) {
	f.calls++
	f.follow = follow
	f.since = filter.Since
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(strings.NewReader(f.payload)), nil
}
func diagnosticReceipt() testRunReceipt {
	report := endpointRecoveryReport("failed")
	return testRunReceipt{RunID: "isolated-run", StartedAt: time.Now().Add(-time.Minute), Status: "failed", Error: "checkout missed deadline", Steps: []testScenarioStepEvidence{{Name: "load", Load: report}}}
}
func diagnosticFrame(at time.Time, line string) string {
	body, _ := json.Marshal(api.LogEvent{WrittenAt: at.Format(time.RFC3339Nano), Stream: "stderr", Line: line})
	return "event: log\ndata: " + string(body) + "\n\n"
}
func TestRecoveryFailureDiagnosticsBoundedRedactedAndLinked(t *testing.T) {
	receipt := diagnosticReceipt()
	phase := &receipt.Steps[0].Load.ChaosPhases[2]
	phase.StartedAt = time.Now().Add(-10 * time.Second)
	phase.FinishedAt = time.Now()
	secret := "private-credential-value"
	client := &recoveryDiagnosticFake{payload: diagnosticFrame(time.Now().Add(-7*time.Second), "dependency refused "+secret) + diagnosticFrame(time.Now().Add(-7*time.Second), "password=another-value") + diagnosticFrame(time.Now().Add(-time.Hour), "old message")}
	collectTestRecoveryFailureDiagnostics(t.Context(), client, &receipt, map[string]string{"app": "isolated-app"}, []string{secret})
	bundle := receipt.FailureDiagnostics
	if bundle == nil || len(bundle.Assertions) != 1 || bundle.Assertions[0].Request != "checkout" || phase.StepRecovery["checkout"].DiagnosticsRef != bundle.ID {
		t.Fatalf("assertion link missing: %+v", bundle)
	}
	if client.follow || client.since == "" || len(bundle.Workloads["app"].Logs) != 2 {
		t.Fatalf("wrong log window/page: %+v", bundle)
	}
	body, _ := json.Marshal(bundle)
	if strings.Contains(string(body), secret) || strings.Contains(string(body), "another-value") || strings.Contains(string(body), "old message") || len(body) > testFailureDiagnosticMaxBytes {
		t.Fatal("unsafe diagnostic bundle")
	}
	if receipt.Status != "failed" || receipt.Error != "checkout missed deadline" {
		t.Fatal("diagnostics changed result")
	}
	path := filepath.Join(t.TempDir(), "report.html")
	if err := writeTestHTMLReport(path, "", []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `href="#`+bundle.ID+`"`) || !strings.Contains(string(body), `id="`+bundle.ID+`"`) || strings.Contains(string(body), secret) {
		t.Fatal("HTML diagnostic link/evidence missing or unsafe")
	}
	path = filepath.Join(t.TempDir(), "report.xml")
	if err := writeTestJUnit(path, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var suite testJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil || suite.Failures != 1 {
		t.Fatalf("JUnit: %v", err)
	}
	var decoded testRunReceipt
	if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded); err != nil || decoded.FailureDiagnostics == nil || decoded.FailureDiagnostics.ID != bundle.ID || decoded.Error != receipt.Error {
		t.Fatalf("JUnit diagnostic evidence missing: %v", err)
	}
	client.payload = strings.Repeat(diagnosticFrame(time.Now().Add(-7*time.Second), strings.Repeat("x", 900)), 1000)
	collectTestRecoveryFailureDiagnostics(t.Context(), client, &receipt, map[string]string{"app": "isolated-app"}, nil)
	logs := receipt.FailureDiagnostics.Workloads["app"]
	if !logs.Truncated || len(logs.Logs) > 20 {
		t.Fatal("log limits not enforced")
	}
}
func TestRecoveryFailureDiagnosticsCollectionFailuresPreserveResult(t *testing.T) {
	receipt := diagnosticReceipt()
	client := &recoveryDiagnosticFake{err: errors.New("token=unprintable-credential")}
	collectTestRecoveryFailureDiagnostics(t.Context(), client, &receipt, map[string]string{"app": "isolated-app"}, nil)
	body, _ := json.Marshal(receipt.FailureDiagnostics)
	if receipt.Status != "failed" || receipt.Error != "checkout missed deadline" || strings.Contains(string(body), "unprintable") || receipt.FailureDiagnostics.Workloads["app"].CollectionError == "" {
		t.Fatal("collection failure altered or contaminated result")
	}
	receipt = diagnosticReceipt()
	receipt.Steps[0].Load.ChaosPhases[2].StepRecovery["checkout"].Passed = true
	client.calls = 0
	collectTestRecoveryFailureDiagnostics(t.Context(), client, &receipt, map[string]string{"app": "isolated-app"}, nil)
	if client.calls != 0 || receipt.FailureDiagnostics != nil {
		t.Fatal("healthy recovery collected logs")
	}
}
func TestRecoveryFailureDiagnosticsInterruptsLogStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	receipt := diagnosticReceipt()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	collectTestRecoveryFailureDiagnostics(ctx, NewClient(server.URL, "operator-secret"), &receipt, map[string]string{"app": "isolated-app"}, nil)
	if time.Since(start) > time.Second || receipt.FailureDiagnostics.Workloads["app"].CollectionError == "" || receipt.Error != "checkout missed deadline" {
		t.Fatal("stream timeout failed")
	}
}
func TestRecoveryFailureDiagnosticsWorkloadAndWireLimits(t *testing.T) {
	receipt := diagnosticReceipt()
	slugs := map[string]string{}
	for i := 0; i < 30; i++ {
		slugs[fmt.Sprintf("workload-%02d", i)] = "isolated-app"
	}
	client := &recoveryDiagnosticFake{payload: "event: log\ndata: " + strings.Repeat("z", 128*1024) + "\n\n"}
	collectTestRecoveryFailureDiagnostics(t.Context(), client, &receipt, slugs, nil)
	if client.calls > testFailureDiagnosticMaxWorkloads || !receipt.FailureDiagnostics.Truncated {
		t.Fatal("workload cap not enforced")
	}
	body, _ := json.Marshal(receipt.FailureDiagnostics)
	if len(body) > testFailureDiagnosticMaxBytes {
		t.Fatal("bundle byte cap exceeded")
	}
}

func TestRecoveryFailureDiagnosticsCredentialVariants(t *testing.T) {
	values := testDiagnosticRedactions([]string{"Bearer small-credential", "postgres://worker:db-pass@localhost/cache", "prefix-password", "prefix"})
	for _, secret := range []string{"small-credential", "db-pass", "prefix-password", "c21hbGwtY3JlZGVudGlhbA=="} {
		if strings.Contains(redactTestDiagnosticText("failed: "+secret, values, 1024), secret) {
			t.Fatalf("known credential variant retained")
		}
	}
	result := redactTestDiagnosticText(strings.Repeat("é", 1000), nil, 1023)
	if len(result) > 1023 {
		t.Fatal("UTF8 bound exceeded")
	}
}

type excessiveDiagnosticMetadata struct{ testDiagnosticsFake }

func (excessiveDiagnosticMetadata) GetAppWakeTimeline(context.Context, string, api.AppWakeTimelineOptions) (api.AppWakeTimelineResponse, error) {
	rows := make([]api.WakeTimelineJSONRow, 50)
	for i := range rows {
		rows[i] = api.WakeTimelineJSONRow{WakeID: "wake-1", Trigger: "raw-sensitive-trigger", Method: "restore"}
	}
	return api.AppWakeTimelineResponse{Rows: rows}, nil
}
func (excessiveDiagnosticMetadata) ListAppDebugRequestsWithOptions(context.Context, string, api.DebugTelemetryListOptions) (api.DebugTelemetryListResponse, error) {
	rows := make([]api.DebugTelemetryRequestItem, 50)
	for i := range rows {
		rows[i] = api.DebugTelemetryRequestItem{RequestID: strings.Repeat("x", 2000), Route: "/exports/known-key?credential=raw-secret", Method: "GET", Status: 503}
	}
	return api.DebugTelemetryListResponse{Requests: rows}, nil
}
func TestRecoveryFailureDiagnosticsMetadataBoundsAndRedaction(t *testing.T) {
	results := collectTestDiagnostics(t.Context(), excessiveDiagnosticMetadata{}, map[string]string{"app": "isolated-app"}, []string{"known-key"})
	result := results["app"]
	if len(result.Wakes) != 20 || len(result.Requests) != 30 || result.Wakes[0].Trigger != "" || len(result.Requests[0].RequestID) > 128 {
		t.Fatal("metadata bounds not enforced")
	}
	body, _ := json.Marshal(results)
	for _, value := range []string{"raw-sensitive-trigger", "known-key", "raw-secret", "credential="} {
		if strings.Contains(string(body), value) {
			t.Fatal("sensitive metadata retained")
		}
	}
}
