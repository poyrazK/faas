// adr: 650 — output limits remain failures even after HTTP 200 headers.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func responseLimitDiagnosticFixture(t *testing.T, id int, metadata string, streamed bool) string {
	t.Helper()
	outcome, status := "rejected", 502
	if streamed {
		outcome, status = "aborted", 200
	}
	line := dataAPIDiagnosticFixture(t, id, "rest", outcome, status, 20, "response_too_large")
	return strings.TrimSuffix(line, "}") + `,"response_limit":` + metadata + "}"
}

func TestDataAPIResponseLimitDiagnosticsAndRedaction(t *testing.T) {
	c := newDataAPIDiagnosticsCollector("", "")
	for i, source := range []string{"content_length", "stream"} {
		metadata := fmt.Sprintf(`{"source":%q,"limit_bytes":%d,"observed_at_least_bytes":%d,"private":"private-row-value"}`, source, api.DataAPIMaxResponseBytes, api.DataAPIMaxResponseBytes+1)
		line := responseLimitDiagnosticFixture(t, i+1, metadata, i == 1)
		c.observe(line)
		c.observe(line)
	}
	c.finish()
	if c.report.Partial || c.report.Failures.ResponseTooLarge != 2 || c.report.Summary.Errors != 2 || c.report.Summary.ServerErrors != 1 || c.report.Summary.Aborted != 1 || c.report.Duplicates != 2 {
		t.Fatalf("incorrect response failure counts: %+v", c.report)
	}
	lookup := newDataAPIDiagnosticsCollector("", "00000000-0000-4000-8000-000000000001")
	lookup.observe(responseLimitDiagnosticFixture(t, 1, fmt.Sprintf(`{"source":"stream","limit_bytes":%d,"observed_at_least_bytes":%d,"private":"private-row-value"}`, api.DataAPIMaxResponseBytes, api.DataAPIMaxResponseBytes+1), true))
	lookup.finish()
	encoded, err := json.Marshal(lookup.report)
	if err != nil || strings.Contains(string(encoded), "private-row-value") || len(lookup.report.Matches) != 1 || lookup.report.Matches[0].ResponseLimit.Source != "stream" {
		t.Fatal("response-limit lookup leaked or lost safe metadata")
	}
	stdout, _, restore := swapIO(t)
	defer restore()
	if err := writeDataAPIDiagnostics(c.report); err != nil || !strings.Contains(stdout.String(), "Response size limits: 2 observed") {
		t.Fatalf("missing text diagnostics: %s (%v)", stdout.String(), err)
	}
}

func TestDataAPIResponseLimitRejectsMalformedMetadata(t *testing.T) {
	valid := fmt.Sprintf(`{"source":"stream","limit_bytes":%d,"observed_at_least_bytes":%d}`, api.DataAPIMaxResponseBytes, api.DataAPIMaxResponseBytes+1)
	for _, metadata := range []string{`{}`, `{"source":"private-row-value"}`, strings.ReplaceAll(valid, "stream", "private-row-value"), strings.ReplaceAll(valid, fmt.Sprint(api.DataAPIMaxResponseBytes+1), "1"), strings.ReplaceAll(valid, fmt.Sprint(api.DataAPIMaxResponseBytes+1), "null"), strings.ReplaceAll(valid, fmt.Sprint(api.DataAPIMaxResponseBytes), "-1")} {
		c := newDataAPIDiagnosticsCollector("", "")
		c.observe(responseLimitDiagnosticFixture(t, 1, metadata, true))
		if !c.report.Partial || c.report.Invalid != 1 {
			t.Fatal("malformed response limit was accepted")
		}
	}
	for _, line := range []string{
		strings.ReplaceAll(responseLimitDiagnosticFixture(t, 1, valid, false), "response_too_large", "token_invalid"),
		strings.ReplaceAll(responseLimitDiagnosticFixture(t, 1, valid, false), `"status":502`, `"status":200`),
	} {
		if _, _, err := parseDataAPIRequestRecord(line); err == nil {
			t.Fatal("inconsistent response-limit metadata was accepted")
		}
	}
}
