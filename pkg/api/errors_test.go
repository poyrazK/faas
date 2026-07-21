// Tests for pkg/api/errors.go: Problem construction, error chains, and the
// RFC 7807 write path. These functions are the platform's single error
// contract (spec §Conventions); every error shape we ship must come from here.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAsProblem(t *testing.T) {
	t.Run("nil error returns nil", func(t *testing.T) {
		if got := AsProblem(nil); got != nil {
			t.Errorf("AsProblem(nil) = %v, want nil", got)
		}
	})

	t.Run("non-problem error returns nil", func(t *testing.T) {
		if got := AsProblem(errors.New("plain")); got != nil {
			t.Errorf("AsProblem(plain) = %v, want nil", got)
		}
	})

	t.Run("direct problem returns same pointer", func(t *testing.T) {
		p := NewProblem(http.StatusForbidden, "x", "X", "x")
		if got := AsProblem(p); got != p {
			t.Errorf("AsProblem did not return the same *Problem")
		}
	})

	t.Run("wrapped problem is unwrapped", func(t *testing.T) {
		p := NewProblem(http.StatusForbidden, "x", "X", "x")
		wrapped := fmt.Errorf("context: %w", p)
		if got := AsProblem(wrapped); got != p {
			t.Errorf("AsProblem(wrapped) did not unwrap to the inner *Problem")
		}
	})
}

func TestProblem_Error(t *testing.T) {
	cases := []struct {
		name   string
		detail string
		want   string
	}{
		{"no detail", "", "code_only"},
		{"with detail", "the why", "code_only: the why"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &Problem{Code: "code_only", Detail: tc.detail}
			if got := p.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProblem_Chaining(t *testing.T) {
	p := NewProblem(http.StatusForbidden, "x", "X", "d").
		WithLimit(100, 5).
		WithDocs("https://docs.x")

	if p.Limit == nil || *p.Limit != 100 {
		t.Errorf("Limit = %v, want 100", p.Limit)
	}
	if p.Observed == nil || *p.Observed != 5 {
		t.Errorf("Observed = %v, want 5", p.Observed)
	}
	if p.DocsURL != "https://docs.x" {
		t.Errorf("DocsURL = %q", p.DocsURL)
	}
}

func TestWriteProblem(t *testing.T) {
	p := NewProblem(http.StatusForbidden, "x", "X", "d").
		WithLimit(10, 2).
		WithDocs("https://docs.x")
	rr := httptest.NewRecorder()
	WriteProblem(rr, p)

	if got := rr.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}

	var got Problem
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Code != "x" || got.Title != "X" || got.Detail != "d" {
		t.Errorf("decoded = %+v", got)
	}
	if got.Limit == nil || *got.Limit != 10 {
		t.Errorf("decoded Limit = %v", got.Limit)
	}
}

func TestErrPlanLimitApps(t *testing.T) {
	l := MustLimitsFor(PlanHobby) // 5 apps
	p := ErrPlanLimitApps(l, 6)
	if p.Status != http.StatusForbidden {
		t.Errorf("Status = %d, want 403", p.Status)
	}
	if p.Code != CodePlanLimitApps {
		t.Errorf("Code = %q, want %q", p.Code, CodePlanLimitApps)
	}
	if !strings.Contains(p.Detail, "hobby") || !strings.Contains(p.Detail, "5") {
		t.Errorf("Detail %q should name plan + cap", p.Detail)
	}
	if p.Limit == nil || *p.Limit != 5 {
		t.Errorf("Limit = %v, want 5", p.Limit)
	}
	if p.Observed == nil || *p.Observed != 6 {
		t.Errorf("Observed = %v, want 6", p.Observed)
	}
	if !strings.Contains(p.DocsURL, "docs.DOMAIN") {
		t.Errorf("DocsURL = %q", p.DocsURL)
	}
}

func TestErrPlanLimitRAM(t *testing.T) {
	l := MustLimitsFor(PlanPro) // 512 MB
	p := ErrPlanLimitRAM(l, 1024)
	if p.Code != CodePlanLimitRAM {
		t.Errorf("Code = %q", p.Code)
	}
	if p.Limit == nil || *p.Limit != 512 {
		t.Errorf("Limit = %v, want 512", p.Limit)
	}
	if !strings.Contains(p.Detail, "512") || !strings.Contains(p.Detail, "1024") {
		t.Errorf("Detail %q should name cap + request", p.Detail)
	}
}

func TestErrPlanLimitConcurrency(t *testing.T) {
	l := MustLimitsFor(PlanScale) // 20 concurrent
	p := ErrPlanLimitConcurrency(l, 25)
	if p.Status != http.StatusTooManyRequests {
		t.Errorf("Status = %d, want 429", p.Status)
	}
	if p.Code != CodePlanLimitConcur {
		t.Errorf("Code = %q", p.Code)
	}
	if p.Limit == nil || *p.Limit != 20 {
		t.Errorf("Limit = %v, want 20", p.Limit)
	}
}

func TestErrCapacity(t *testing.T) {
	p := ErrCapacity("no RAM headroom")
	if p.Status != http.StatusServiceUnavailable {
		t.Errorf("Status = %d, want 503", p.Status)
	}
	if p.Code != CodeCapacity {
		t.Errorf("Code = %q", p.Code)
	}
	if p.Detail != "no RAM headroom" {
		t.Errorf("Detail = %q", p.Detail)
	}
	if !strings.Contains(p.DocsURL, "status.DOMAIN") {
		t.Errorf("DocsURL = %q", p.DocsURL)
	}
}

func TestErrSourceTooLarge(t *testing.T) {
	l := MustLimitsFor(PlanFree) // 100 MB cap
	p := ErrSourceTooLarge(l, 150*1024*1024)
	if p.Code != CodeSourceTooLarge {
		t.Errorf("Code = %q", p.Code)
	}
	if p.Status != http.StatusRequestEntityTooLarge {
		t.Errorf("Status = %d, want 413", p.Status)
	}
	if p.Limit == nil || *p.Limit != int64(100*1024*1024) {
		t.Errorf("Limit = %v", p.Limit)
	}
}

// Stable error code sanity: every Code* constant is non-empty and unique —
// clients branch on these strings so they must not drift silently.
func TestCodeConstants_UniqueAndNonEmpty(t *testing.T) {
	codes := []string{
		CodePlanLimitApps, CodePlanLimitRAM, CodePlanLimitConcur,
		CodeSourceTooLarge, CodeAppLayerTooBig,
		CodeBuildUndetected, CodeBuildOOM, CodeBuildTimeout,
		CodeQuotaExhausted, CodeBillingPastDue, CodeCapacity,
		CodeUnauthorized, CodeNotFound, CodeValidation,
		CodeImageNotFound, CodeImageEgressDenied, CodeImageManifestInvalid,
		CodeCliAuthPending, CodeCliAuthUnavailable,
	}
	seen := make(map[string]bool)
	for _, c := range codes {
		if c == "" {
			t.Error("found empty code constant")
			continue
		}
		if seen[c] {
			t.Errorf("duplicate code constant: %q", c)
		}
		seen[c] = true
	}
}

// TestStatusForCode_ImageCodes locks the HTTP status mapping for the three
// puller-side codes (ADR-021). 422 for the validation-class failures
// (image_not_found, image_manifest_invalid); 403 for the security-class
// egress denial (distinct from 422 / 404 so customers can tell the policy
// block apart from a 404). Unknown codes must default to 500.
func TestStatusForCode_ImageCodes(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{CodeImageNotFound, http.StatusUnprocessableEntity},
		{CodeImageManifestInvalid, http.StatusUnprocessableEntity},
		{CodeImageEgressDenied, http.StatusForbidden},
		{"totally_made_up_code", http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			if got := StatusForCode(tc.code); got != tc.want {
				t.Errorf("StatusForCode(%q) = %d, want %d", tc.code, got, tc.want)
			}
		})
	}
}

// --- ValidateAppConfig (dto.go) ---------------------------------------------

func TestValidateAppConfig(t *testing.T) {
	cases := []struct {
		name     string
		plan     Plan
		ramMB    int
		maxConc  int
		wantNil  bool
		wantCode string
	}{
		{name: "under caps (hobby)", plan: PlanHobby, ramMB: 256, maxConc: 2, wantNil: true},
		{name: "ram over cap (hobby: 256)", plan: PlanHobby, ramMB: 512, maxConc: 2, wantCode: CodePlanLimitRAM},
		{name: "concur over cap (hobby: 2)", plan: PlanHobby, ramMB: 128, maxConc: 5, wantCode: CodePlanLimitConcur},
		// RAM is checked first, so over-cap RAM + over-cap concurrency returns the RAM error.
		{name: "both over — RAM wins", plan: PlanHobby, ramMB: 9999, maxConc: 9999, wantCode: CodePlanLimitRAM},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := MustLimitsFor(tc.plan)
			p := ValidateAppConfig(l, tc.ramMB, tc.maxConc)
			if tc.wantNil {
				if p != nil {
					t.Errorf("ValidateAppConfig = %+v, want nil", p)
				}
				return
			}
			if p == nil {
				t.Fatalf("ValidateAppConfig = nil, want code %q", tc.wantCode)
			}
			if p.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", p.Code, tc.wantCode)
			}
		})
	}
}
