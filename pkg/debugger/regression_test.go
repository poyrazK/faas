package debugger

// adr: 127 — error-rate route regressions (amendment): thresholds and kinds.

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestErrorRateRegressed(t *testing.T) {
	for name, tc := range map[string]struct {
		baseline, current RouteErrors
		want              bool
	}{
		"new failures":                 {RouteErrors{200, 0}, RouteErrors{200, 30}, true},
		"too few failed requests":      {RouteErrors{200, 0}, RouteErrors{100, 9}, false},
		"too little baseline traffic":  {RouteErrors{49, 0}, RouteErrors{200, 30}, false},
		"too little current traffic":   {RouteErrors{200, 0}, RouteErrors{49, 30}, false},
		"rise under five points":       {RouteErrors{1000, 20}, RouteErrors{1000, 60}, false},
		"rise above five but under 2x": {RouteErrors{1000, 100}, RouteErrors{1000, 160}, false},
		"rate doubled and +5 points":   {RouteErrors{1000, 50}, RouteErrors{1000, 110}, true},
		"steady failure rate":          {RouteErrors{1000, 300}, RouteErrors{1000, 300}, false},
		"improvement":                  {RouteErrors{1000, 300}, RouteErrors{1000, 10}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := ErrorRateRegressed(tc.baseline, tc.current); got != tc.want {
				t.Fatalf("ErrorRateRegressed(%+v, %+v) = %v, want %v", tc.baseline, tc.current, got, tc.want)
			}
		})
	}
}

func TestRouteErrorsRatePct(t *testing.T) {
	for _, tc := range []struct {
		in   RouteErrors
		want float64
	}{
		{RouteErrors{0, 0}, 0},
		{RouteErrors{3, 1}, 33.33},
		{RouteErrors{8, 1}, 12.5},
		// Errors cannot exceed requests; a malformed count saturates at 100%.
		{RouteErrors{5, 9}, 100},
	} {
		if got := tc.in.RatePct(); got != tc.want {
			t.Errorf("%+v.RatePct() = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestRegressionKind(t *testing.T) {
	cases := []struct {
		latency, errors       bool
		want                  string
		hasErrors, hasLatency bool
	}{
		{false, false, "", false, true},
		{true, false, RegressionKindLatency, false, true},
		{false, true, RegressionKindErrors, true, false},
		{true, true, RegressionKindLatencyAndErrors, true, true},
	}
	for _, tc := range cases {
		got := RegressionKind(tc.latency, tc.errors)
		if got != tc.want || RegressionHasErrors(got) != tc.hasErrors || RegressionHasLatency(got) != tc.hasLatency {
			t.Errorf("RegressionKind(%v, %v) = %q (errors=%v latency=%v)", tc.latency, tc.errors, got, RegressionHasErrors(got), RegressionHasLatency(got))
		}
	}
}

func TestSynthesizeNamesErrorRegression(t *testing.T) {
	evidence := api.DebugRequestEvidenceResponse{
		Request: api.DebugTelemetryRequestItem{Status: 200},
		Regression: &api.DebugRegressionItem{Route: "POST /pay", Kind: RegressionKindErrors, Factor: "1.00",
			ErrorRatePct: 12.5, ErrorRateBasePct: 0.4, ErrorCount: 61},
	}
	got := Synthesize(evidence)
	if got.Diagnosis != "error_regression" || got.Confidence != "high" {
		t.Fatalf("diagnosis = %q/%q, want error_regression/high", got.Diagnosis, got.Confidence)
	}
	if want := "This route fails 12.5% of requests since the deployment, up from 0.4%."; got.Headline != want {
		t.Fatalf("headline = %q, want %q", got.Headline, want)
	}
	if !hasFinding(got, "error_regression") || hasFinding(got, "performance_regression") {
		t.Fatalf("an errors-only regression reports only the error finding: %+v", got.Findings)
	}
}

func TestSynthesizeLatencyAndErrorsReportsBoth(t *testing.T) {
	evidence := api.DebugRequestEvidenceResponse{
		Request: api.DebugTelemetryRequestItem{Status: 200},
		Regression: &api.DebugRegressionItem{Route: "POST /pay", Kind: RegressionKindLatencyAndErrors, Factor: "2.10",
			P95MS: 420, P95BaseMS: 200, AffectedCount: 150, ErrorRatePct: 9, ErrorRateBasePct: 1, ErrorCount: 45},
	}
	got := Synthesize(evidence)
	if got.Diagnosis != "error_regression" {
		t.Fatalf("diagnosis = %q, want error_regression (failures outrank slowness)", got.Diagnosis)
	}
	if !hasFinding(got, "error_regression") || !hasFinding(got, "performance_regression") {
		t.Fatalf("findings = %+v, want both regression findings", got.Findings)
	}
}

// A regression stored before error-rate detection has no kind and stays a
// latency regression.
func TestSynthesizeLegacyRegressionIsLatency(t *testing.T) {
	got := Synthesize(api.DebugRequestEvidenceResponse{
		Request:    api.DebugTelemetryRequestItem{Status: 200},
		Regression: &api.DebugRegressionItem{Route: "GET /", Factor: "1.50", P95MS: 300, P95BaseMS: 200},
	})
	if got.Diagnosis != "performance_regression" || hasFinding(got, "error_regression") {
		t.Fatalf("legacy regression = %q %+v", got.Diagnosis, got.Findings)
	}
}

func TestEvidenceLogsKeepsLastLinesInOrder(t *testing.T) {
	base := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	var events []EvidenceLogInput
	// The store returns newest first; 25 lines, two with sub-second times.
	for i := 24; i >= 0; i-- {
		events = append(events, EvidenceLogInput{OccurredAt: base.Add(time.Duration(i) * 100 * time.Millisecond), Source: "runtime", Level: "INFO", Message: "line " + string(rune('a'+i)) + "\n"})
	}
	got := EvidenceLogs(events)
	if len(got) != EvidenceLogMaxLines {
		t.Fatalf("lines = %d, want %d", len(got), EvidenceLogMaxLines)
	}
	if got[0].Message != "line f" || got[len(got)-1].Message != "line y" {
		t.Fatalf("kept %q..%q, want the last 20 lines in order", got[0].Message, got[len(got)-1].Message)
	}
	for i := 1; i < len(got); i++ {
		prev, _ := time.Parse(time.RFC3339Nano, got[i-1].OccurredAt)
		cur, _ := time.Parse(time.RFC3339Nano, got[i].OccurredAt)
		if cur.Before(prev) {
			t.Fatalf("lines out of order at %d: %s before %s", i, got[i].OccurredAt, got[i-1].OccurredAt)
		}
	}
	if got[0].Level != "info" {
		t.Fatalf("level = %q, want lower-cased", got[0].Level)
	}
}

func TestEvidenceLogsBoundsMessages(t *testing.T) {
	got := EvidenceLogs([]EvidenceLogInput{{OccurredAt: time.Now(), Source: "runtime", Message: strings.Repeat("x", 4096)}})
	if len(got) != 1 || len(got[0].Message) > EvidenceLogMaxMessageBytes {
		t.Fatalf("message length = %d, want ≤ %d", len(got[0].Message), EvidenceLogMaxMessageBytes)
	}
	if empty := EvidenceLogs(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("no events must encode as an empty array, got %#v", empty)
	}
}

func TestSynthesizeReportsErrorLogs(t *testing.T) {
	evidence := api.DebugRequestEvidenceResponse{
		Request: api.DebugTelemetryRequestItem{Status: 200},
		Logs: []api.DebugEvidenceLogLine{
			{Level: "info", Message: "starting"},
			{Level: "error", Message: "payment declined"},
			{Level: "FATAL", Message: "giving up"},
		},
	}
	got := Synthesize(evidence)
	var detail string
	for _, finding := range got.Findings {
		if finding.Code == "error_logs" {
			detail = finding.Detail
		}
	}
	if !strings.Contains(detail, "2 error-level") {
		t.Fatalf("error_logs finding = %q, want 2 error-level lines", detail)
	}
	if strings.Contains(detail, "payment declined") {
		t.Fatalf("finding prose must not quote log messages: %q", detail)
	}
	if hasFinding(Synthesize(api.DebugRequestEvidenceResponse{Logs: []api.DebugEvidenceLogLine{{Level: "warn"}}}), "error_logs") {
		t.Fatal("warnings are not error lines")
	}
}
