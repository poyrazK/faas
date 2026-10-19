package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func concurrencyLimitDiagnosticFixture(t *testing.T, id int, metadata string) string {
	t.Helper()
	line := dataAPIDiagnosticFixture(t, id, "rest", "rejected", 429, 10, "data_api_concurrency_limited")
	return strings.TrimSuffix(line, "}") + `,"concurrency_limit":` + metadata + "}"
}

func TestDataAPIConcurrencyLimitDiagnosticsAndRedaction(t *testing.T) {
	c := newDataAPIDiagnosticsCollector("", "")
	for i, metadata := range []string{
		`{"reason":"subject","max_concurrent":1,"active":1,"cooling_down":0,"retry_after_seconds":1,"subject":"private-subject"}`,
		`{"reason":"subject","max_concurrent":2,"active":0,"cooling_down":2,"retry_after_seconds":15}`,
		`{"reason":"tracking_capacity","max_concurrent":1,"active":0,"cooling_down":0,"retry_after_seconds":1}`,
	} {
		line := concurrencyLimitDiagnosticFixture(t, i+1, metadata)
		c.observe(line)
		c.observe(line)
	}
	c.finish()
	if c.report.Partial || c.report.Failures.ConcurrencyLimited != 3 || c.report.Failures.RateLimited != 0 || c.report.Failures.Unavailable != 0 || c.report.Failures.Overloaded != 0 || c.report.Summary.Errors != 3 || c.report.Summary.ServerErrors != 0 || c.report.Duplicates != 3 {
		t.Fatalf("concurrency limits were misclassified: %+v", c.report)
	}
	lookup := newDataAPIDiagnosticsCollector("", "00000000-0000-4000-8000-000000000001")
	lookup.observe(concurrencyLimitDiagnosticFixture(t, 1, `{"reason":"subject","max_concurrent":1,"active":0,"cooling_down":1,"retry_after_seconds":15,"token":"private-token"}`))
	lookup.finish()
	body, err := json.Marshal(lookup.report)
	if err != nil || strings.Contains(string(body), "private-") || len(lookup.report.Matches) != 1 || *lookup.report.Matches[0].ConcurrencyLimit.CoolingDown != 1 {
		t.Fatal("lookup lost safe concurrency metadata or leaked identity")
	}
	stdout, _, restore := swapIO(t)
	defer restore()
	if err := writeDataAPIDiagnostics(c.report); err != nil || !strings.Contains(stdout.String(), "User concurrency limits: 3 observed") {
		t.Fatalf("missing concurrency-limit text: %s (%v)", stdout.String(), err)
	}
}

func TestDataAPIConcurrencyLimitRejectsInvalidAccountingAndPolicy(t *testing.T) {
	valid := `{"reason":"subject","max_concurrent":1,"active":1,"cooling_down":0,"retry_after_seconds":1}`
	for _, metadata := range []string{
		`{}`, `null`, strings.Replace(valid, "subject", "private-subject", 1),
		strings.Replace(valid, `"max_concurrent":1`, `"max_concurrent":0`, 1),
		strings.Replace(valid, `"max_concurrent":1`, `"max_concurrent":3`, 1),
		strings.Replace(valid, `"active":1`, `"active":null`, 1),
		strings.Replace(valid, `"active":1`, `"active":0`, 1),
		strings.Replace(valid, `"active":1`, `"active":-1`, 1),
		strings.Replace(valid, `"cooling_down":0`, `"cooling_down":null`, 1),
		strings.Replace(valid, `"cooling_down":0`, `"cooling_down":1`, 1),
		strings.Replace(valid, `"retry_after_seconds":1`, `"retry_after_seconds":0`, 1),
		strings.Replace(valid, `"retry_after_seconds":1`, `"retry_after_seconds":16`, 1),
		strings.Replace(valid, `"retry_after_seconds":1`, `"retry_after_seconds":15`, 1),
		strings.Replace(valid, `"reason":"subject"`, `"reason":"tracking_capacity"`, 1),
	} {
		c := newDataAPIDiagnosticsCollector("", "")
		c.observe(concurrencyLimitDiagnosticFixture(t, 1, metadata))
		if !c.report.Partial || c.report.Invalid != 1 {
			t.Fatalf("accepted malformed concurrency metadata: %s", metadata)
		}
	}
	line := concurrencyLimitDiagnosticFixture(t, 1, valid)
	for _, invalid := range []string{strings.Replace(line, "data_api_concurrency_limited", "data_api_rate_limited", 1), strings.Replace(line, `"status":429`, `"status":503`, 1), strings.Replace(line, `"route":"rest"`, `"route":"health"`, 1), strings.Replace(line, `"outcome":"rejected"`, `"outcome":"completed"`, 1)} {
		if _, _, err := parseDataAPIRequestRecord(invalid); err == nil {
			t.Fatal("accepted inconsistent concurrency-limit metadata")
		}
	}
	if _, _, err := parseDataAPIRequestRecord(strings.Replace(line, `"outcome":"rejected"`, `"outcome":"aborted"`, 1)); err != nil {
		t.Fatal("an interrupted rejection must retain its metadata", err)
	}
	if _, _, err := parseDataAPIRequestRecord(dataAPIDiagnosticFixture(t, 1, "rest", "rejected", 429, 1, "data_api_concurrency_limited")); err == nil {
		t.Fatal("accepted rejection without metadata")
	}
	if _, _, err := parseDataAPIRequestRecord(dataAPIDiagnosticFixture(t, 1, "rest", "completed", 200, 1, nil)); err != nil {
		t.Fatal("legacy request metadata must remain readable", err)
	}
}
