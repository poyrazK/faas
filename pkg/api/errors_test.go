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
	"regexp"
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
	// Pins the docs host. docs.gregale.dev was never deployed (DNS
	// resolves, every path 404s), so customer-facing links live on
	// the platform host instead. This assertion is the tripwire for
	// a future host rotation.
	if !strings.HasPrefix(p.DocsURL, "https://gregale.dev/docs") {
		t.Errorf("DocsURL = %q, want the live docs base", p.DocsURL)
	}
	if !strings.Contains(p.DocsURL, "plans") {
		t.Errorf("DocsURL = %q, want it to name the plans topic", p.DocsURL)
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
	if !strings.Contains(p.DocsURL, "gregale.dev/status") {
		t.Errorf("DocsURL = %q", p.DocsURL)
	}
}

// TestErrWaitForWarm locks the 503 + Retry-After wire shape for
// the per-app scale-out cooldown (issue #462 / PR-D). Distinct
// from TestErrPlanLimitConcurrency (429, no Retry-After) and
// from TestErrCapacity (503, no Retry-After). The Retry-After
// header is the canonical UX: clients that consult the header
// can back off without polling the 503 + body alone.
func TestErrWaitForWarm(t *testing.T) {
	l := MustLimitsFor(PlanScale) // 20 concurrent
	p := ErrWaitForWarm(5, l, 7)
	if p.Status != http.StatusServiceUnavailable {
		t.Errorf("Status = %d, want 503", p.Status)
	}
	if p.Code != CodeWaitForWarm {
		t.Errorf("Code = %q, want %q", p.Code, CodeWaitForWarm)
	}
	if p.Limit == nil || *p.Limit != 5 {
		t.Errorf("Limit = %v, want 5 (cooldown seconds)", p.Limit)
	}
	if p.Observed == nil || *p.Observed != 7 {
		t.Errorf("Observed = %v, want 7 (live instances)", p.Observed)
	}
	if !strings.Contains(p.DocsURL, "scaling-policy#cooldown") {
		t.Errorf("DocsURL = %q, want /scaling-policy#cooldown", p.DocsURL)
	}
	if got := p.HasHeader("Retry-After"); len(got) != 1 || got[0] != "5" {
		t.Errorf("HasHeader(Retry-After) = %v, want [5]", got)
	}
	if !strings.Contains(p.Detail, "5 more second") {
		t.Errorf("Detail = %q, want to name the cooldown remaining seconds", p.Detail)
	}
}

// TestErrWaitForWarm_BoundsAtOne pins the floor: cooldownS <= 0
// is treated as 1 so the wire Retry-After header is always a
// positive integer. Without this, a clock-skew-induced zero
// (Postgres now() ahead of the engine's time.Now()) would emit
// Retry-After: 0, which RFC 7231 §7.1.3 forbids.
func TestErrWaitForWarm_BoundsAtOne(t *testing.T) {
	l := MustLimitsFor(PlanScale)
	for _, cooldownS := range []int{-5, 0, 1} {
		p := ErrWaitForWarm(cooldownS, l, 3)
		if got := p.HasHeader("Retry-After"); len(got) != 1 || got[0] != "1" {
			t.Errorf("cooldownS=%d: HasHeader(Retry-After) = %v, want [1]", cooldownS, got)
		}
	}
}

// TestErrWaitForWarm_FlushedOnWire pins the WithHeader chain
// end-to-end: gatewayd-internal's writeWakeError surface WriteProblem
// flushes the Retry-After header from extraHeaders. Gaps here
// would silently drop the wire hint and the 503 would look
// indistinguishable from a permanent failure.
func TestErrWaitForWarm_FlushedOnWire(t *testing.T) {
	l := MustLimitsFor(PlanScale)
	p := ErrWaitForWarm(7, l, 1)

	rr := httptest.NewRecorder()
	WriteProblem(rr, p)

	if got := rr.Header().Get("Retry-After"); got != "7" {
		t.Errorf("Retry-After = %q, want \"7\"", got)
	}
	// extraHeaders must not leak into the JSON body.
	body := rr.Body.String()
	if strings.Contains(body, "Retry-After") {
		t.Errorf("Retry-After must not appear in the JSON body: %s", body)
	}
	if strings.Contains(body, "extraHeaders") {
		t.Errorf("extraHeaders must not appear on the wire: %s", body)
	}
}

// TestErrBillingNotImplemented pins the 501 mapping for issue #279
// (Paddle's Refund stub returns billing.ErrNotImplemented; an apid
// handler that calls Provider.Refund dispatches on errors.Is(err,
// billing.ErrNotImplemented) and routes here). The 501 status is
// load-bearing — it's the signal that lets an operator pick a
// billing backend that supports the surface they need.
func TestErrBillingNotImplemented(t *testing.T) {
	p := ErrBillingNotImplemented("Provider does not implement Refund")
	if p.Status != http.StatusNotImplemented {
		t.Errorf("Status = %d, want 501", p.Status)
	}
	if p.Code != CodeBillingNotImplemented {
		t.Errorf("Code = %q, want %q", p.Code, CodeBillingNotImplemented)
	}
	if !strings.Contains(p.DocsURL, "billing/providers") {
		t.Errorf("DocsURL = %q, want /billing/providers", p.DocsURL)
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
		CodePlanLimitApps, CodePlanLimitRAM, CodePlanLimitConcur, CodeInvalidAppCPU,
		CodeSourceTooLarge, CodeAppLayerTooBig,
		CodeBuildUndetected, CodeBuildOOM, CodeBuildTimeout,
		CodeQuotaExhausted, CodeBillingPastDue, CodeCapacity,
		CodeUnauthorized, CodeNotFound, CodeValidation,
		CodeImageNotFound, CodeImageEgressDenied, CodeImageManifestInvalid,
		CodeCliAuthPending, CodeCliAuthUnavailable,
		CodeInvalidCredentials, CodeEmailNotVerified, CodeEmailVerificationRequired, CodePasswordTooWeak,
		CodeResetTokenInvalid, CodeResetTokenExpired, CodeAccountExists,
		// IAM-6 / ADR-061 (issue #190). One cluster per ADR table.
		CodeOrgNotFound, CodeOrgSlugInvalid, CodeOrgSlugTaken,
		CodeOrgMemberCapExceeded, CodeOrgInvitationCapExceeded,
		CodeOrgRoleForbidden, CodeOrgAlreadyMember,
		CodeOrgInvitationInvalid, CodeOrgInvitationExpired,
		CodeOrgLastOwner, CodeOrgPersonalImmutable, CodeOrgAPIKeyRequiresOrg,
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

// TestStatusForCode_AuthCodes locks the HTTP status mapping for the
// dashboard-auth codes added in PR #1 (issue #165, ADR-032). Both
// invalid_credentials and email_not_verified must collapse to 401 so
// the dashboard form can render a single "sign in failed" copy for
// both; the surface must NOT distinguish them, otherwise an attacker
// can probe for which case fired. The authenticated verification gate
// is a 403 because the caller is known but cannot perform the action yet.
func TestStatusForCode_AuthCodes(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{CodeInvalidCredentials, http.StatusUnauthorized},
		{CodeEmailNotVerified, http.StatusUnauthorized},
		{CodeEmailVerificationRequired, http.StatusForbidden},
		{CodePasswordTooWeak, http.StatusBadRequest},
		{CodeAccountExists, http.StatusBadRequest},
		{CodeResetTokenInvalid, http.StatusGone},
		{CodeResetTokenExpired, http.StatusGone},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			if got := StatusForCode(tc.code); got != tc.want {
				t.Errorf("StatusForCode(%q) = %d, want %d", tc.code, got, tc.want)
			}
		})
	}
}

// TestStatusForCode_WaitForWarm locks the 503 inverse-status mapping
// for the per-app scale-out cooldown (issue #462 / PR-D). The
// inverse lift lives in pkg/grpcerr.FromStatus: when a wake-gate
// Problem crosses the gRPC boundary it loses the 503 (both 429 and
// 503 map to ResourceExhausted on the gRPC side), and the lift
// re-derives the HTTP status via StatusForCode. A gap here would
// silently demote the 503 to 429 / 500 on the customer-facing wire.
func TestStatusForCode_WaitForWarm(t *testing.T) {
	if got := StatusForCode(CodeWaitForWarm); got != http.StatusServiceUnavailable {
		t.Errorf("StatusForCode(CodeWaitForWarm) = %d, want %d", got, http.StatusServiceUnavailable)
	}
}

// TestStatusForCode_CodeAdmissionRefused (issue #561) — pins the
// spend-cap pause-workload HTTP status mapping. The per-cap
// rejection lifts to HTTP 402 Payment Required, NOT 429 (that's
// rate-limit) and NOT 503 (the backend is healthy; the customer
// turned the bucket off). The StatusForCode reverse mapping is
// what the gRPC boundary consults (grpcerr.FromStatus) so a
// regression here would silently demote the 402 to 429 / 500 on
// the customer-facing wire.
func TestStatusForCode_CodeAdmissionRefused(t *testing.T) {
	if got := StatusForCode(CodeAdmissionRefused); got != http.StatusPaymentRequired {
		t.Errorf("StatusForCode(CodeAdmissionRefused) = %d, want %d", got, http.StatusPaymentRequired)
	}
	// Cross-check: ErrAdmissionRefused carries the same Status on
	// the *Problem it constructs. A regression that drifts the
	// builder away from StatusForCode would surface here.
	p := ErrAdmissionRefused(123, 100)
	if p.Status != http.StatusPaymentRequired {
		t.Errorf("ErrAdmissionRefused.Status = %d, want %d", p.Status, http.StatusPaymentRequired)
	}
	if p.Code != CodeAdmissionRefused {
		t.Errorf("ErrAdmissionRefused.Code = %q, want %q", p.Code, CodeAdmissionRefused)
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

// TestErrLongPollTimeout pins the status + code for the long-poll
// handlers. Move 2 adds two long-poll surfaces (sync invoke,
// queueReceive) which both surface this on timeout.
func TestErrLongPollTimeout(t *testing.T) {
	p := ErrLongPollTimeout()
	if p.Status != http.StatusGatewayTimeout {
		t.Errorf("Status = %d, want 504", p.Status)
	}
	if p.Code != "long_poll_timeout" {
		t.Errorf("Code = %q, want long_poll_timeout", p.Code)
	}
	if !strings.Contains(p.DocsURL, "event-driven") {
		t.Errorf("DocsURL = %q, want contains event-driven", p.DocsURL)
	}
}

// TestErrInvalidScheduledAt pins the 400 + code for delayed-task create
// with a past scheduled_at. Distinct code so the CLI can suggest a
// future timestamp without parsing prose.
func TestErrInvalidScheduledAt(t *testing.T) {
	p := ErrInvalidScheduledAt()
	if p.Status != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", p.Status)
	}
	if p.Code != "invalid_scheduled_at" {
		t.Errorf("Code = %q, want invalid_scheduled_at", p.Code)
	}
	if !strings.Contains(p.DocsURL, "event-driven") {
		t.Errorf("DocsURL = %q, want contains event-driven", p.DocsURL)
	}
}

// TestErrPlanQueueDepth_DistinctFromDelayedCap pins that the two
// per-app caps (queue depth, delayed-task cap) have distinct codes so
// the dashboard can render different guidance ("drain your queue" vs
// "schedule for later").
func TestErrPlanQueueDepth_DistinctFromDelayedCap(t *testing.T) {
	l := MustLimitsFor(PlanHobby)
	q := ErrPlanQueueDepth(l.MaxQueueDepth, l.MaxQueueDepth+1)
	d := ErrPlanDelayedTasksCap(l.MaxDelayedTasksPerApp, l.MaxDelayedTasksPerApp+1)
	if q.Code == d.Code {
		t.Errorf("queue + delayed share code %q — must be distinct", q.Code)
	}
	if q.Status != http.StatusForbidden {
		t.Errorf("queue status = %d, want 403", q.Status)
	}
	if d.Status != http.StatusForbidden {
		t.Errorf("delayed status = %d, want 403", d.Status)
	}
}

// TestProblem_CheckoutExtensionsMarshalled asserts the provider-neutral
// and legacy checkout URL + tx_id extensions serialize correctly on a 402 — and stay
// omitted on a Problem that doesn't carry them (so the Stripe-default
// response shape is unchanged).
//
// Pin for PR #3 / ADR-025: the 402 Problem carries at most one of
// BillingPortalURL or PaddleCheckoutURL on the wire; the JSON omitempty
// tag guarantees the unused field drops out cleanly.
func TestProblem_CheckoutExtensionsMarshalled(t *testing.T) {
	t.Parallel()

	t.Run("both fields populated", func(t *testing.T) {
		p := &Problem{
			Code:              CodePayment,
			CheckoutURL:       "https://checkout.polar.sh/session-1",
			PaddleCheckoutURL: "https://sandbox.paddle.example/checkout/abc",
			TxID:              "txn_test_123",
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(b), `"paddle_checkout_url":"https://sandbox.paddle.example/checkout/abc"`) {
			t.Errorf("missing paddle_checkout_url in JSON: %s", b)
		}
		if !strings.Contains(string(b), `"checkout_url":"https://checkout.polar.sh/session-1"`) {
			t.Errorf("missing checkout_url in JSON: %s", b)
		}
		if !strings.Contains(string(b), `"tx_id":"txn_test_123"`) {
			t.Errorf("missing tx_id in JSON: %s", b)
		}
	})

	t.Run("empty fields omitted", func(t *testing.T) {
		p := &Problem{Code: CodePayment}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(b), "paddle_checkout_url") {
			t.Errorf("paddle_checkout_url should be omitted when empty: %s", b)
		}
		if strings.Contains(string(b), "checkout_url") {
			t.Errorf("checkout_url should be omitted when empty: %s", b)
		}
		if strings.Contains(string(b), "tx_id") {
			t.Errorf("tx_id should be omitted when empty: %s", b)
		}
	})

	t.Run("stripe default unchanged", func(t *testing.T) {
		// The pre-PR-#3 402 Problem carried BillingPortalURL only.
		// PR #3 must not change that wire shape — pin it.
		p := &Problem{
			Code:             CodePayment,
			BillingPortalURL: "https://billing.example/?acct=acc_1",
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(b), `"billing_portal_url":"https://billing.example/?acct=acc_1"`) {
			t.Errorf("missing billing_portal_url in JSON: %s", b)
		}
		if strings.Contains(string(b), "paddle_checkout_url") {
			t.Errorf("paddle_checkout_url must not appear on the Stripe 402: %s", b)
		}
		if strings.Contains(string(b), "checkout_url") {
			t.Errorf("checkout_url must not appear on the portal-only 402: %s", b)
		}
		if strings.Contains(string(b), "tx_id") {
			t.Errorf("tx_id must not appear on the Stripe 402: %s", b)
		}
	})
}

// TestProblem_WithHeaderFlushedOnWire pins the review finding #1a
// fix on PR #322: the build-attestation transient-I/O branch on
// pkg/sched.Engine.Wake/Prime wraps the storage error in a
// *api.Problem with a Retry-After: 5 header via WithHeader, and
// gatewayd-internal's writeWakeError surfaces it via api.WriteProblem.
// The chain — WithHeader → extraHeaders → WriteProblem → wire
// header — must be complete; gaps here would silently drop the
// Retry-After and the 503 would look indistinguishable from a
// permanent failure.
func TestProblem_WithHeaderFlushedOnWire(t *testing.T) {
	p := NewProblem(503, CodeCapacity, "transient", "retry shortly").
		WithHeader("Retry-After", "5").
		WithHeader("X-Faas-Wake-Reason", "verifier_io")

	rr := httptest.NewRecorder()
	WriteProblem(rr, p)

	if got := rr.Header().Get("Retry-After"); got != "5" {
		t.Errorf("Retry-After = %q, want \"5\"", got)
	}
	if got := rr.Header().Get("X-Faas-Wake-Reason"); got != "verifier_io" {
		t.Errorf("X-Faas-Wake-Reason = %q, want \"verifier_io\"", got)
	}
	// extraHeaders must not leak into the JSON body (it's an
	// HTTP-layer concept, not an RFC 7807 field).
	body := rr.Body.String()
	if strings.Contains(body, "extraHeaders") {
		t.Errorf("extraHeaders must not appear on the wire: %s", body)
	}
	if strings.Contains(body, "Retry-After") {
		t.Errorf("Retry-After must not appear in the JSON body: %s", body)
	}
}

// TestProblem_WithHeaderAccumulates pins that multiple WithHeader
// calls on the same key add rather than overwrite — gatewayd-internal's
// downstream chain (header set, then header Add) would silently
// drop the second value if this were a plain map assign.
func TestProblem_WithHeaderAccumulates(t *testing.T) {
	p := NewProblem(503, "x", "x", "x").
		WithHeader("X-Demo", "a").
		WithHeader("X-Demo", "b")

	if got := p.HasHeader("X-Demo"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("HasHeader(X-Demo) = %v, want [a b]", got)
	}
}

// TestErrPlanAlertRulesNotAllowed pins the 402 returned when the
// customer's plan doesn't unlock alert rules at all (Free today).
// Issue #396 / ADR-045 PR 3.
func TestErrPlanAlertRulesNotAllowed(t *testing.T) {
	p := ErrPlanAlertRulesNotAllowed(PlanFree)
	if p.Status != http.StatusPaymentRequired {
		t.Errorf("Status = %d, want 402", p.Status)
	}
	if p.Code != CodePlanAlertRulesNotAllowed {
		t.Errorf("Code = %q, want %q", p.Code, CodePlanAlertRulesNotAllowed)
	}
	if !strings.Contains(p.Detail, "free") {
		t.Errorf("Detail = %q, want to name the plan", p.Detail)
	}
}

// TestErrPlanAlertRuleQuota pins the 403 returned when the plan
// unlocks alert rules but the per-app or per-account cap was reached.
// Mirrors TestErrPlanCronQuota in spirit (issue #396 / ADR-045 PR 3).
func TestErrPlanAlertRuleQuota(t *testing.T) {
	cases := []struct {
		scope   string
		wantSub string
	}{
		{"app", "this app"},
		{"account", "this account"},
	}
	for _, tc := range cases {
		t.Run(tc.scope, func(t *testing.T) {
			p := ErrPlanAlertRuleQuota(PlanPro, tc.scope, 10, 10)
			if p.Status != http.StatusForbidden {
				t.Errorf("Status = %d, want 403", p.Status)
			}
			if p.Code != CodePlanAlertRuleQuota {
				t.Errorf("Code = %q, want %q", p.Code, CodePlanAlertRuleQuota)
			}
			if p.Limit == nil || *p.Limit != 10 {
				t.Errorf("Limit = %v, want 10", p.Limit)
			}
			if p.Observed == nil || *p.Observed != 10 {
				t.Errorf("Observed = %v, want 10", p.Observed)
			}
			if !strings.Contains(p.Detail, tc.wantSub) {
				t.Errorf("Detail = %q, want to contain %q", p.Detail, tc.wantSub)
			}
		})
	}
}

// TestErrAlertRuleInvalid pins the 400 for shape violations (closed-
// set drift, NaN threshold, cooldown out of band, SSRF rejected,
// metric-family swap rejected). Issue #396 / ADR-045 PR 3.
func TestErrAlertRuleInvalid(t *testing.T) {
	p := ErrAlertRuleInvalid("metric family cannot change; delete and recreate")
	if p.Status != http.StatusBadRequest {
		t.Errorf("Status = %d, want 400", p.Status)
	}
	if p.Code != CodeAlertRuleInvalid {
		t.Errorf("Code = %q, want %q", p.Code, CodeAlertRuleInvalid)
	}
	if !strings.Contains(p.Detail, "delete and recreate") {
		t.Errorf("Detail = %q, want to carry the reason", p.Detail)
	}
}

// TestStatusForCode_AlertRules pins the inverse-status table for the
// three new alert-rule codes so pkg/grpcerr.FromStatus can lift a gRPC
// code back into a Problem with the right HTTP status (defense in
// depth against future code additions that forget the switch case).
func TestStatusForCode_AlertRules(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{CodePlanAlertRulesNotAllowed, http.StatusPaymentRequired},
		{CodePlanAlertRuleQuota, http.StatusForbidden},
		{CodeAlertRuleInvalid, http.StatusBadRequest},
	}
	for _, tc := range cases {
		if got := StatusForCode(tc.code); got != tc.want {
			t.Errorf("StatusForCode(%q) = %d, want %d", tc.code, got, tc.want)
		}
	}
}

// TestErrPlanLogArchiveNotAllowed pins the 402 the gatewayd-internal
// archive read-back handler emits when the customer's plan has
// LogArchiveEnabled() == false (Free today, issue #562). The
// shape mirrors TestErrPlanAlertRulesNotAllowed: stable code
// + a Plan-named detail line. The dashboard surfaces the upsell
// copy ("upgrade to Hobby or above to query historical logs
// from object storage") straight from p.Detail.
func TestErrPlanLogArchiveNotAllowed(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby} {
		t.Run(string(p), func(t *testing.T) {
			prob := ErrPlanLogArchiveNotAllowed(p)
			if prob.Status != http.StatusPaymentRequired {
				t.Errorf("Status = %d, want 402", prob.Status)
			}
			if prob.Code != CodePlanLogArchiveNotAllowed {
				t.Errorf("Code = %q, want %q", prob.Code, CodePlanLogArchiveNotAllowed)
			}
			if !strings.Contains(prob.Detail, string(p)) {
				t.Errorf("Detail = %q, want to name plan %q", prob.Detail, p)
			}
		})
	}
}

// TestStatusForCode_LogArchive locks the 402 inverse-status mapping
// for the plan_log_archive_not_allowed code (issue #562 / PR-B).
// A future drift between CodePlanLogArchiveNotAllowed and the 402
// gate fires here, not in production.
func TestStatusForCode_LogArchive(t *testing.T) {
	if got := StatusForCode(CodePlanLogArchiveNotAllowed); got != http.StatusPaymentRequired {
		t.Errorf("StatusForCode(%q) = %d, want 402", CodePlanLogArchiveNotAllowed, got)
	}
}

// TestStatusForCode_OrgCodes pins the inverse-status table for the
// 12 IAM-6 / ADR-061 org codes. The cluster is split across 404
// (slug not found — IDOR convention), 422 (slug shape), 409
// (slug taken / already member / last owner / personal immutable /
// legacy key), 410 (invitation invalid / expired), and 403 (role
// / member cap / invitation cap). pkg/grpcerr.FromStatus consumes
// this table on the gRPC boundary; a gap here would silently
// downgrade a 403 to 500 on the customer-facing wire.
func TestStatusForCode_OrgCodes(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{CodeOrgNotFound, http.StatusNotFound},
		{CodeOrgSlugInvalid, http.StatusUnprocessableEntity},
		{CodeOrgSlugTaken, http.StatusConflict},
		{CodeOrgMemberCapExceeded, http.StatusForbidden},
		{CodeOrgInvitationCapExceeded, http.StatusForbidden},
		{CodeOrgRoleForbidden, http.StatusForbidden},
		{CodeOrgAlreadyMember, http.StatusConflict},
		{CodeOrgInvitationInvalid, http.StatusGone},
		{CodeOrgInvitationExpired, http.StatusGone},
		{CodeOrgLastOwner, http.StatusConflict},
		{CodeOrgPersonalImmutable, http.StatusConflict},
		{CodeOrgAPIKeyRequiresOrg, http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			if got := StatusForCode(tc.code); got != tc.want {
				t.Errorf("StatusForCode(%q) = %d, want %d", tc.code, got, tc.want)
			}
		})
	}
}

// TestErrOrgNotFound wires the constructor + status for the most
// common org error. The 404 status mirrors the IDOR convention so
// cross-tenant access returns 404, never 403. Other code
// constructors are pinned via their respective tests / handlers
// once PR 5 lands; this test seeds the surface.
func TestErrOrgNotFound(t *testing.T) {
	p := ErrOrgNotFound("acme")
	if p.Status != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", p.Status)
	}
	if p.Code != CodeOrgNotFound {
		t.Errorf("Code = %q, want %q", p.Code, CodeOrgNotFound)
	}
	if !strings.Contains(p.Detail, "acme") {
		t.Errorf("Detail = %q, want to name the slug", p.Detail)
	}
	if !strings.Contains(p.DocsURL, "/orgs") {
		t.Errorf("DocsURL = %q, want to point at /orgs", p.DocsURL)
	}
}

// TestErrOrgMemberCapExceeded pins the 403 + limit/observed pair
// for the per-plan member cap. Mirrors TestErrPlanCronQuota in
// spirit so the SDK's error decoder can share the quota-reached
// branch.
func TestErrOrgMemberCapExceeded(t *testing.T) {
	p := ErrOrgMemberCapExceeded(10, 10)
	if p.Status != http.StatusForbidden {
		t.Errorf("Status = %d, want 403", p.Status)
	}
	if p.Code != CodeOrgMemberCapExceeded {
		t.Errorf("Code = %q, want %q", p.Code, CodeOrgMemberCapExceeded)
	}
	if p.Limit == nil || *p.Limit != 10 {
		t.Errorf("Limit = %v, want 10", p.Limit)
	}
	if p.Observed == nil || *p.Observed != 10 {
		t.Errorf("Observed = %v, want 10", p.Observed)
	}
}

// TestErrOrgMemberCapExceeded_ZeroBoundary pins the wire shape at
// the (limit=0, observed=0) boundary. WithLimit binds the field
// non-nil even for zero, so the JSON serialises `"limit": 0`
// (not omitted). A future refactor that tries to omit zero would
// silently break the SDK quota decoder on a brand-new account or
// an org whose plan cap is genuinely 0.
func TestErrOrgMemberCapExceeded_ZeroBoundary(t *testing.T) {
	p := ErrOrgMemberCapExceeded(0, 0)
	if p.Limit == nil {
		t.Fatal("Limit must remain non-nil even at zero (SDK depends on *int64 presence)")
	}
	if *p.Limit != 0 || *p.Observed != 0 {
		t.Errorf("Limit/Observed = %d/%d, want 0/0", *p.Limit, *p.Observed)
	}
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"limit":0`) {
		t.Errorf("JSON must serialise zero Limit (SDK decoder relies on it): %s", body)
	}
	if !strings.Contains(string(body), `"observed":0`) {
		t.Errorf("JSON must serialise zero Observed: %s", body)
	}
}

// TestErrOrgInvitationExpired pins the 410 + code for the
// one-shot invitation lifecycle. Mirrors TestErrResetTokenExpired
// so the dashboard's "link expired" copy reuses the same pattern.
func TestErrOrgInvitationExpired(t *testing.T) {
	p := ErrOrgInvitationExpired()
	if p.Status != http.StatusGone {
		t.Errorf("Status = %d, want 410", p.Status)
	}
	if p.Code != CodeOrgInvitationExpired {
		t.Errorf("Code = %q, want %q", p.Code, CodeOrgInvitationExpired)
	}
}

// TestErrOrgLastOwner pins the 409 + reusable copy for the
// own-points-on-the-only-owner guard. Detail names the
// remediation (transfer ownership) so dashboards can render
// guidance without parsing prose.
func TestErrOrgLastOwner(t *testing.T) {
	p := ErrOrgLastOwner()
	if p.Status != http.StatusConflict {
		t.Errorf("Status = %d, want 409", p.Status)
	}
	if p.Code != CodeOrgLastOwner {
		t.Errorf("Code = %q, want %q", p.Code, CodeOrgLastOwner)
	}
	if !strings.Contains(p.Detail, "transfer") {
		t.Errorf("Detail = %q, want to mention ownership transfer", p.Detail)
	}
}

// TestErrOrgSlugInvalid pins the 422 + shape copy. The detail
// must echo the shared OrgSlugPattern constant so PR 5's handler
// regex and this rejection copy cannot drift silently.
func TestErrOrgSlugInvalid(t *testing.T) {
	p := ErrOrgSlugInvalid("leading dash")
	if p.Status != http.StatusUnprocessableEntity {
		t.Errorf("Status = %d, want 422", p.Status)
	}
	if p.Code != CodeOrgSlugInvalid {
		t.Errorf("Code = %q, want %q", p.Code, CodeOrgSlugInvalid)
	}
	if !strings.Contains(p.Detail, OrgSlugPattern) {
		t.Errorf("Detail = %q, want to carry OrgSlugPattern %q", p.Detail, OrgSlugPattern)
	}
	if !strings.Contains(p.Detail, "leading dash") {
		t.Errorf("Detail = %q, want to carry the reason", p.Detail)
	}
}

// TestOrgSlugPattern covers the well-formed boundary cases and a few
// malformed inputs. PR 5's handler validator will compile the same
// constant so a tightening here breaks the validator in tests rather
// than on the wire. Keep the boundary cases tight: 3 chars (floor),
// 32 chars (ceiling), single internal dash, leading digit (allowed),
// trailing dash (rejected), uppercase (rejected).
func TestOrgSlugPattern(t *testing.T) {
	cases := []struct {
		in      string
		want    bool
		comment string
	}{
		{"abc", true, "floor length"},
		{"a-b", true, "single internal dash"},
		{"a1", false, "below floor length"},
		{strings.Repeat("a", MaxOrgSlugLen), true, "ceiling length"},
		{strings.Repeat("a", MaxOrgSlugLen+1), false, "above ceiling"},
		{"abc-", false, "trailing dash"},
		{"-abc", false, "leading dash"},
		{"ABC", false, "uppercase"},
		{"abc_def", false, "underscore"},
		{"abc.def", false, "dot"},
		{"1bc", true, "leading digit allowed"},
		{"ab1", true, "trailing digit allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.in+"/"+tc.comment, func(t *testing.T) {
			if got := regexp.MustCompile(OrgSlugPattern).MatchString(tc.in); got != tc.want {
				t.Errorf("OrgSlugPattern.MatchString(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
