// adr: 650 — user rate-limit reports preserve safe coverage and failure counts.
package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"strings"
	"testing"
)

func rateLimitDiagnosticFixture(t *testing.T, id int, metadata string) string {
	t.Helper()
	line := dataAPIDiagnosticFixture(t, id, "rest", "rejected", 429, 10, "data_api_rate_limited")
	return strings.TrimSuffix(line, "}") + `,"rate_limit":` + metadata + "}"
}

func TestDataAPIRateLimitDiagnostics(t *testing.T) {
	c := main.NewDataAPIDiagnosticsCollector("", "")
	for i, reason := range []string{"subject", "tracking_capacity"} {
		line := rateLimitDiagnosticFixture(t, i+1, `{"reason":"`+reason+`","per_second":10,"burst":20,"retry_after_seconds":1,"subject":"private-subject","token":"private-token"}`)
		c.Observe(line)
		c.Observe(line)
	}
	c.Finish()
	if (*c.Report()).Partial || (*c.Report()).Failures.RateLimited != 2 || (*c.Report()).Failures.Unavailable != 0 || (*c.Report()).Failures.Overloaded != 0 || (*c.Report()).Summary.Errors != 2 || (*c.Report()).Summary.ServerErrors != 0 || (*c.Report()).Duplicates != 2 {
		t.Fatalf("rate limits were misclassified: %+v", (*c.Report()))
	}
	lookup := main.NewDataAPIDiagnosticsCollector("", "00000000-0000-4000-8000-000000000001")
	lookup.Observe(rateLimitDiagnosticFixture(t, 1, `{"reason":"tracking_capacity","per_second":1000,"burst":1000,"retry_after_seconds":1,"subject":"private-subject"}`))
	lookup.Finish()
	encoded, err := json.Marshal((*lookup.Report()))
	if err != nil || strings.Contains(string(encoded), "private-") || len((*lookup.Report()).Matches) != 1 || (*lookup.Report()).Matches[0].RateLimit.Reason != "tracking_capacity" {
		t.Fatal("lookup lost safe rate-limit metadata or leaked a subject")
	}
	stdout, _, restore := main.SwapIO(t)
	defer restore()
	if err := main.WriteDataAPIDiagnostics((*c.Report())); err != nil || !strings.Contains(stdout.String(), "User rate limits: 2 observed") {
		t.Fatalf("missing rate-limit text: %s (%v)", stdout.String(), err)
	}
}

func TestDataAPIRateLimitRejectsMalformedMetadata(t *testing.T) {
	valid := `{"reason":"subject","per_second":10,"burst":20,"retry_after_seconds":1}`
	for _, metadata := range []string{`{}`, `null`, strings.ReplaceAll(valid, "subject", "private-subject"), strings.ReplaceAll(valid, `"per_second":10`, `"per_second":0`), strings.ReplaceAll(valid, `"burst":20`, `"burst":1001`), strings.ReplaceAll(valid, `"burst":20`, `"burst":null`), strings.ReplaceAll(valid, `"retry_after_seconds":1`, `"retry_after_seconds":2`)} {
		c := main.NewDataAPIDiagnosticsCollector("", "")
		c.Observe(rateLimitDiagnosticFixture(t, 1, metadata))
		if !(*c.Report()).Partial || (*c.Report()).Invalid != 1 {
			t.Fatalf("malformed policy was accepted: %s", metadata)
		}
	}
	line := rateLimitDiagnosticFixture(t, 1, valid)
	for _, invalid := range []string{strings.ReplaceAll(line, "data_api_rate_limited", "data_api_overloaded"), strings.ReplaceAll(line, `"status":429`, `"status":503`), strings.ReplaceAll(line, `"route":"rest"`, `"route":"health"`), strings.ReplaceAll(line, `"outcome":"rejected"`, `"outcome":"completed"`)} {
		if _, _, err := main.ParseDataAPIRequestRecord(invalid); err == nil {
			t.Fatal("inconsistent rate-limit record was accepted")
		}
	}
	if _, _, err := main.ParseDataAPIRequestRecord(strings.ReplaceAll(line, `"outcome":"rejected"`, `"outcome":"aborted"`)); err != nil {
		t.Fatal("an interrupted rate-limit response must retain safe diagnostics", err)
	}
	if _, _, err := main.ParseDataAPIRequestRecord(dataAPIDiagnosticFixture(t, 1, "rest", "completed", 200, 1, nil)); err != nil {
		t.Fatal("legacy requests without rate-limit metadata must remain readable", err)
	}
}
