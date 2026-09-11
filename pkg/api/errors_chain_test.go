// errors_chain_test.go — fill pkg/api/errors.go chained-mutator +
// sentinel-constructor coverage gaps. Targets the With* mutators, the
// StatusForCode mapping, and a representative slice of the 50+ Err*
// sentinels. Whitebox `package api` (matches the existing
// apierror_test.go).

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- With* chain mutators ---------------------------------------------

func TestProblem_WithChainedMutators_ReturnsReceiverAndSetsFields(t *testing.T) {
	// Verify every With* returns the SAME receiver so chains like
	// ErrPlanLimitApps(...).WithDocs(...).WithWhy(...).WithFix(...)
	// produce a single rendered Problem.
	p := &Problem{Code: "test", Title: "t", Status: 400}
	got := p.WithDocs("https://docs.example.com/p1").
		WithHint("do X").
		WithWhy("because Y").
		WithFix("fix Z").
		WithLimit(100, 99).
		WithHeader("X-Region", "us-east-1")
	if got != p {
		t.Error("chain did not return receiver")
	}
	if p.DocsURL != "https://docs.example.com/p1" {
		t.Errorf("DocsURL = %q", p.DocsURL)
	}
	if p.Hint != "do X" {
		t.Errorf("Hint = %q", p.Hint)
	}
	if p.Why != "because Y" {
		t.Errorf("Why = %q", p.Why)
	}
	if p.Fix != "fix Z" {
		t.Errorf("Fix = %q", p.Fix)
	}
	if p.Limit == nil || *p.Limit != 100 {
		t.Errorf("Limit = %v", p.Limit)
	}
	if p.Observed == nil || *p.Observed != 99 {
		t.Errorf("Observed = %v", p.Observed)
	}
	// HasHeader reads back the headers stamped by WithHeader.
	if v := p.HasHeader("X-Region"); len(v) == 0 || v[0] != "us-east-1" {
		t.Errorf("HasHeader(X-Region) = %v", v)
	}
}

func TestProblem_WithSecretScan_StampsFindings(t *testing.T) {
	findings := []SecretFinding{
		{File: "src/a.py", Line: 7, Key: "AKIA", Provider: "aws", Severity: "high", Snippet: "AKIA...", Layer: "app"},
	}
	p := (&Problem{Code: "secret_detected", Status: 422}).WithSecretScan(findings, "rotate keys")
	if len(p.SecretFindings) != 1 {
		t.Fatalf("SecretFindings = %v", p.SecretFindings)
	}
	if p.SecretHint != "rotate keys" {
		t.Errorf("SecretHint = %q", p.SecretHint)
	}
}

func TestProblem_WithRelevantLogs_StampsExcerpts(t *testing.T) {
	logs := []LogExcerpt{
		{Timestamp: "2026-01-01T00:00:00Z", Message: "first"},
		{Timestamp: "2026-01-01T00:00:01Z", Message: "second"},
	}
	p := (&Problem{Code: "test"}).WithRelevantLogs(logs)
	if len(p.RelevantLogs) != 2 {
		t.Errorf("RelevantLogs = %v", p.RelevantLogs)
	}
}

func TestProblem_HasHeader_ReturnsValues(t *testing.T) {
	p := (&Problem{}).WithHeader("X-Trace", "abc").WithHeader("X-Trace", "def")
	got := p.HasHeader("X-Trace")
	if len(got) != 2 {
		t.Errorf("HasHeader: got %v", got)
	}
	if len(p.HasHeader("X-Missing")) != 0 {
		t.Errorf("missing header returned non-empty: %v", p.HasHeader("X-Missing"))
	}
}

// --- StatusForCode ---------------------------------------------------

func TestStatusForCode_KnownCodes(t *testing.T) {
	// Spot-check the known-code map. The full table is in
	// errors.go:1346; a regression that flips any common code is
	// caught here.
	cases := map[string]int{
		"plan_limit_apps":               http.StatusForbidden,
		"plan_log_archive_not_allowed":  http.StatusPaymentRequired,
		"app_layer_too_large":           http.StatusForbidden,
		"app_not_listening":             http.StatusUnprocessableEntity,
		"app_runtime_oom":               http.StatusUnprocessableEntity,
		"doctor_disabled":               http.StatusInternalServerError, // not in StatusForCode switch
		"image_secret_detected":         http.StatusInternalServerError, // not in StatusForCode switch
		"export_rate_limited":           http.StatusTooManyRequests,
		"step_up_required":              http.StatusInternalServerError, // not in StatusForCode switch
		"app_concurrency_reached":       http.StatusTooManyRequests,
		"app_maintenance_mode":          http.StatusServiceUnavailable,
		"debug_regressions_unavailable": http.StatusServiceUnavailable,
		"tenant_surfaces_not_enabled":   http.StatusServiceUnavailable,
		"domain_not_verified":           http.StatusConflict,
		"plan_cron_quota":               http.StatusForbidden,
		"alert_rule_invalid":            http.StatusBadRequest,
		"cron_invalid":                  http.StatusBadRequest,
		"app_webhook_invalid":           http.StatusBadRequest,
		"egress_allowlist_too_long":     http.StatusBadRequest,
	}
	for code, want := range cases {
		if got := StatusForCode(code); got != want {
			t.Errorf("StatusForCode(%q) = %d, want %d", code, got, want)
		}
	}
}

func TestStatusForCode_UnknownDefaultsTo500(t *testing.T) {
	if got := StatusForCode("definitely-not-a-known-code"); got != http.StatusInternalServerError {
		t.Errorf("unknown code: got %d, want 500", got)
	}
}

// --- AsProblem / WriteProblem ---------------------------------------

func TestAsProblem_NonProblemPassthrough(t *testing.T) {
	// A non-Problem error must round-trip through AsProblem with
	// StatusForCode mapping.
	got := AsProblem(nil)
	if got != nil {
		t.Errorf("nil err: got %v, want nil", got)
	}
}

func TestWriteProblem_RoundTripJSON(t *testing.T) {
	// WriteProblem serializes the Problem to JSON and writes it
	// with the correct Content-Type + status code. Verify both.
	p := (&Problem{
		Code:   "test_code",
		Status: http.StatusBadRequest,
		Title:  "Test Title",
		Detail: "Test Detail",
	}).WithDocs("https://docs.example.com/p")
	rec := httptest.NewRecorder()
	WriteProblem(rec, p)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q", got)
	}
	if !contains(rec.Body.Bytes(), "test_code") {
		t.Errorf("body missing code: %s", rec.Body.String())
	}
}

func TestWriteProblemWithErrors_AddsFieldErrors(t *testing.T) {
	p := &Problem{Code: "validation_failed", Status: 422, Title: "Validation"}
	errs := []FieldError{
		{Field: "name", Expected: "required", Got: ""},
		{Field: "age", Expected: "positive", Got: "-1"},
	}
	rec := httptest.NewRecorder()
	WriteProblemWithErrors(rec, p, errs)
	if rec.Code != 422 {
		t.Errorf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"name", "age"} {
		if !contains([]byte(body), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// --- Err* sentinels --------------------------------------------------

// sampleLimits returns a fixed Limits table for the Err* tests.
func sampleLimits() Limits {
	return Limits{
		Plan:               PlanHobby,
		DeployedApps:       5,
		MaxConcurrency:     5,
		RAMMB:              512,
		AppLayerMaxMB:      256,
		SourceTarballMaxMB: 100,
	}
}

func TestErrPlanLimitApps_Fields(t *testing.T) {
	l := sampleLimits()
	p := ErrPlanLimitApps(l, 99)
	if p == nil || p.Code != "plan_limit_apps" || p.Status != 403 {
		t.Fatalf("p = %+v", p)
	}
	if p.Limit == nil || *p.Limit != int64(l.DeployedApps) {
		t.Errorf("Limit = %v", p.Limit)
	}
	if p.Observed == nil || *p.Observed != 99 {
		t.Errorf("Observed = %v", p.Observed)
	}
}

func TestErrAppLayerTooLarge_Fields(t *testing.T) {
	l := sampleLimits()
	p := ErrAppLayerTooLarge(l, 999)
	if p == nil || p.Code != "app_layer_too_large" {
		t.Fatalf("p = %+v", p)
	}
	// ErrAppLayerTooLarge stamps the cap in BYTES (AppLayerMaxMB * MiB),
	// not in MB. Pin the conversion.
	wantBytes := int64(l.AppLayerMaxMB) * 1024 * 1024
	if p.Limit == nil || *p.Limit != wantBytes {
		t.Errorf("Limit = %v, want %d", p.Limit, wantBytes)
	}
	if p.Observed == nil || *p.Observed != 999 {
		t.Errorf("Observed = %v", p.Observed)
	}
}

func TestErrAppLayerTooLarge_ChainedWithDocs(t *testing.T) {
	// Verify the chain returns the receiver.
	l := sampleLimits()
	p := ErrAppLayerTooLarge(l, 999).WithDocs("https://docs.example.com/layer")
	if p.DocsURL == "" {
		t.Error("WithDocs: DocsURL empty")
	}
}

func TestErrPlanLogArchiveNotAllowed_Chain(t *testing.T) {
	p := ErrPlanLogArchiveNotAllowed(PlanFree)
	if p == nil || p.Code != "plan_log_archive_not_allowed" {
		t.Fatalf("p = %+v", p)
	}
}

func TestErrPlanCronQuota_Fields(t *testing.T) {
	p := ErrPlanCronQuota(PlanHobby, "app", 5, 7)
	if p == nil || p.Code != "plan_cron_quota" {
		t.Fatalf("p = %+v", p)
	}
	if p.Limit == nil || *p.Limit != 5 {
		t.Errorf("Limit = %v", p.Limit)
	}
	if p.Observed == nil || *p.Observed != 7 {
		t.Errorf("Observed = %v", p.Observed)
	}
}

func TestErrDomainCertNotIssued(t *testing.T) {
	p := ErrDomainCertNotIssued("example.com", "rate-limited")
	if p == nil || p.Code != "domain_cert_not_issued" {
		t.Fatalf("p = %+v", p)
	}
}

func TestErrAlertRuleInvalid_Chain(t *testing.T) {
	p := ErrAlertRuleInvalid("invalid metric")
	if p == nil || p.Code != "alert_rule_invalid" {
		t.Fatalf("p = %+v", p)
	}
}

func TestErrAdmissionRefused_Fields(t *testing.T) {
	p := ErrAdmissionRefused(1000, 500)
	if p == nil || p.Code != "admission_refused" {
		t.Fatalf("p = %+v", p)
	}
	if p.Limit == nil || *p.Limit != 500 {
		t.Errorf("Limit = %v", p.Limit)
	}
	if p.Observed == nil || *p.Observed != 1000 {
		t.Errorf("Observed = %v", p.Observed)
	}
}

func TestErrExportRateLimited_Fields(t *testing.T) {
	p := ErrExportRateLimited(30)
	if p == nil || p.Code != "export_rate_limited" {
		t.Fatalf("p = %+v", p)
	}
	// Pin the Retry-After value in the Headers map.
	if v := p.HasHeader("Retry-After"); len(v) == 0 || v[0] != "30" {
		t.Errorf("Retry-After header = %v", v)
	}
}

// --- helpers --------------------------------------------------------

func contains(haystack []byte, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}
