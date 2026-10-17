// adr: 650 — user rate-limit reports preserve safe coverage and failure counts.
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func rateLimitDiagnosticFixture(t *testing.T, id int, metadata string) string {
	t.Helper()
	line := dataAPIDiagnosticFixture(t, id, "rest", "rejected", 429, 10, "data_api_rate_limited")
	return strings.TrimSuffix(line, "}") + `,"rate_limit":` + metadata + "}"
}

func TestDataAPIRateLimitDiagnostics(t *testing.T) {
	c := newDataAPIDiagnosticsCollector("", "")
	for i, reason := range []string{"subject", "tracking_capacity"} {
		line := rateLimitDiagnosticFixture(t, i+1, `{"reason":"`+reason+`","per_second":10,"burst":20,"retry_after_seconds":1,"subject":"private-subject","token":"private-token"}`)
		c.observe(line)
		c.observe(line)
	}
	c.finish()
	if c.report.Partial || c.report.Failures.RateLimited != 2 || c.report.Failures.Unavailable != 0 || c.report.Failures.Overloaded != 0 || c.report.Summary.Errors != 2 || c.report.Summary.ServerErrors != 0 || c.report.Duplicates != 2 {
		t.Fatalf("rate limits were misclassified: %+v", c.report)
	}
	lookup := newDataAPIDiagnosticsCollector("", "00000000-0000-4000-8000-000000000001")
	lookup.observe(rateLimitDiagnosticFixture(t, 1, `{"reason":"tracking_capacity","per_second":1000,"burst":1000,"retry_after_seconds":1,"subject":"private-subject"}`))
	lookup.finish()
	encoded, err := json.Marshal(lookup.report)
	if err != nil || strings.Contains(string(encoded), "private-") || len(lookup.report.Matches) != 1 || lookup.report.Matches[0].RateLimit.Reason != "tracking_capacity" {
		t.Fatal("lookup lost safe rate-limit metadata or leaked a subject")
	}
	stdout, _, restore := swapIO(t)
	defer restore()
	if err := writeDataAPIDiagnostics(c.report); err != nil || !strings.Contains(stdout.String(), "User rate limits: 2 observed") {
		t.Fatalf("missing rate-limit text: %s (%v)", stdout.String(), err)
	}
}

func TestDataAPIRateLimitRejectsMalformedMetadata(t *testing.T) {
	valid := `{"reason":"subject","per_second":10,"burst":20,"retry_after_seconds":1}`
	for _, metadata := range []string{`{}`, `null`, strings.ReplaceAll(valid, "subject", "private-subject"), strings.ReplaceAll(valid, `"per_second":10`, `"per_second":0`), strings.ReplaceAll(valid, `"burst":20`, `"burst":1001`), strings.ReplaceAll(valid, `"burst":20`, `"burst":null`), strings.ReplaceAll(valid, `"retry_after_seconds":1`, `"retry_after_seconds":2`)} {
		c := newDataAPIDiagnosticsCollector("", "")
		c.observe(rateLimitDiagnosticFixture(t, 1, metadata))
		if !c.report.Partial || c.report.Invalid != 1 {
			t.Fatalf("malformed policy was accepted: %s", metadata)
		}
	}
	line := rateLimitDiagnosticFixture(t, 1, valid)
	for _, invalid := range []string{strings.ReplaceAll(line, "data_api_rate_limited", "data_api_overloaded"), strings.ReplaceAll(line, `"status":429`, `"status":503`), strings.ReplaceAll(line, `"route":"rest"`, `"route":"health"`), strings.ReplaceAll(line, `"outcome":"rejected"`, `"outcome":"completed"`)} {
		if _, _, err := parseDataAPIRequestRecord(invalid); err == nil {
			t.Fatal("inconsistent rate-limit record was accepted")
		}
	}
	if _, _, err := parseDataAPIRequestRecord(strings.ReplaceAll(line, `"outcome":"rejected"`, `"outcome":"aborted"`)); err != nil {
		t.Fatal("an interrupted rate-limit response must retain safe diagnostics", err)
	}
	if _, _, err := parseDataAPIRequestRecord(dataAPIDiagnosticFixture(t, 1, "rest", "completed", 200, 1, nil)); err != nil {
		t.Fatal("legacy requests without rate-limit metadata must remain readable", err)
	}
}
