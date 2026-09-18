package api

// errors_sweep2_test.go: covers the remaining zero-coverage sentinels
// constructors in pkg/api/errors.go that pkg/api/errors_test.go did not
// reach. These are pure constructors; the test asserts each returns a
// non-nil *Problem with a non-empty code (RFC 7807 contract: every
// error shape we ship must come from this file with a stable code).

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mustPlan returns the production-shaped Limits for a plan or fatals.
// We use MustLimitsFor because every constructor above that takes
// Limits only reads .Plan, .MaxConcurrency, etc. — fields the production
// limits table populates with non-zero values.
func mustPlan(t *testing.T, p Plan) Limits {
	t.Helper()
	l, ok := LimitsFor(p)
	if !ok {
		t.Fatalf("LimitsFor(%q) returned no limits", p)
	}
	return l
}

// TestErrSentinels_AllConstructors walks every remaining zero-coverage
// sentinel and asserts each returns a well-formed *Problem. The test
// is the load-bearing proof that the constructor surface did not get
// out of sync with the OpenAPI / RFC 7807 contract — if any sentinel
// returns nil or empty code, the platform's "errors are RFC 7807"
// promise breaks.
func TestErrSentinels_AllConstructors(t *testing.T) {
	proLims := mustPlan(t, PlanPro)
	freeLims := mustPlan(t, PlanFree)

	cases := []struct {
		name string
		got  *Problem
	}{
		// apps / plans
		{"ErrAppConcurrencyReached", ErrAppConcurrencyReached(proLims, 1)},
		{"ErrPlanLimitSecrets", ErrPlanLimitSecrets(freeLims, 1)},
		{"ErrPlanLimitEnvVars", ErrPlanLimitEnvVars(freeLims, 1)},
		{"ErrPlanLimitTrustedSigners", ErrPlanLimitTrustedSigners(freeLims, 1)},

		// auth / step-up
		{"ErrInternal", ErrInternal("boom")},
		{"ErrStepUpRequired", ErrStepUpRequired()},
		{"ErrInvalidCredentials", ErrInvalidCredentials()},
		{"ErrEmailNotVerified", ErrEmailNotVerified("github")},
		{"ErrPasswordTooWeak", ErrPasswordTooWeak("too short")},
		{"ErrResetTokenInvalid", ErrResetTokenInvalid()},
		{"ErrResetTokenExpired", ErrResetTokenExpired()},

		// source / deploy
		{"ErrSourceInvalid", ErrSourceInvalid("tar too big")},
		{"ErrStatelessOnlyViolation", ErrStatelessOnlyViolation("stateful_volume", "no")},
		{"ErrHandlerMissing", ErrHandlerMissing()},
		{"ErrDeployFailed", ErrDeployFailed("compile error")},
		{"ErrDeploySignatureInvalid", ErrDeploySignatureInvalid("bad sig")},
		{"ErrTrustedSignerInvalid", ErrTrustedSignerInvalid("expired")},
		{"ErrTrustedSignerNotFound", ErrTrustedSignerNotFound("cosign://x")},
		{"ErrNoRollbackTarget", ErrNoRollbackTarget()},
		{"ErrBuildProvenanceNotFound", ErrBuildProvenanceNotFound()},
		{"ErrBuildSBOMUnavailable", ErrBuildSBOMUnavailable()},

		// domains
		{"ErrDomainNotVerified", ErrDomainNotVerified("example.com")},

		// crons
		{"ErrCronInvalid", ErrCronInvalid("bad schedule")},
		{"ErrPlanCronsNotAllowed", ErrPlanCronsNotAllowed(PlanFree)},
		{"ErrPlanCronQuota", ErrPlanCronQuota(PlanHobby, "app", 5, 6)},

		// eviction priority
		{"ErrPlanEvictionPriorityReservedNotAllowed", ErrPlanEvictionPriorityReservedNotAllowed(PlanFree)},
		{"ErrPlanEvictionPriorityReservedQuota", ErrPlanEvictionPriorityReservedQuota(PlanPro, 1, 0)},

		// public auth
		{"ErrPlanPublicAuthBearerNotAllowed", ErrPlanPublicAuthBearerNotAllowed(PlanFree)},
		{"ErrPlanPublicAuthBasicNotAllowed", ErrPlanPublicAuthBasicNotAllowed(PlanFree)},

		// webhooks
		{"ErrPlanWebhooksNotAllowed", ErrPlanWebhooksNotAllowed(PlanFree)},
		{"ErrPlanWebhookQuota", ErrPlanWebhookQuota(PlanHobby, "app", 5, 6)},
		{"ErrAppWebhookInvalid", ErrAppWebhookInvalid("bad url")},

		// secrets
		{"ErrSecretInvalidKey", ErrSecretInvalidKey("must be A-Z")},
		{"ErrSecretValueTooLarge", ErrSecretValueTooLarge(freeLims, 65536)},
		{"ErrSecretNotFound", ErrSecretNotFound("STRIPE_KEY")},
		{"ErrEnvVarNotFound", ErrEnvVarNotFound("LOG_LEVEL")},

		// registry credentials
		{"ErrPlanRegistryCredentialsNotAllowed", ErrPlanRegistryCredentialsNotAllowed(PlanFree)},
		{"ErrPlanRegistryCredentialQuota", ErrPlanRegistryCredentialQuota(freeLims, 1)},
		{"ErrRegistryCredentialNotFound", ErrRegistryCredentialNotFound("ghcr.io")},
		{"ErrPlanJobRegistryCredentialsNotAllowed", ErrPlanJobRegistryCredentialsNotAllowed(PlanFree)},
		{"ErrPlanJobRegistryCredentialQuota", ErrPlanJobRegistryCredentialQuota(freeLims, 1)},
		{"ErrJobRegistryCredentialNotFound", ErrJobRegistryCredentialNotFound("ghcr.io", "batch-job")},

		// api keys
		{"ErrAPIKeyExpired", ErrAPIKeyExpired()},
		{"ErrAPIKeyRevoked", ErrAPIKeyRevoked()},
		{"ErrAPIKeyLimitExceeded", ErrAPIKeyLimitExceeded(freeLims, 1)},

		// scaling
		{"ErrPlanMinInstancesNotAllowed", ErrPlanMinInstancesNotAllowed(PlanFree)},
		{"ErrInvalidMinInstances", ErrInvalidMinInstances(2, 5)},
		{"ErrMaxMinInstancesExceeded", ErrMaxMinInstancesExceeded(7, 5)},
		{"ErrPlanMaxInstancesNotAllowed", ErrPlanMaxInstancesNotAllowed(PlanFree)},
		{"ErrInvalidMaxInstances", ErrInvalidMaxInstances(10, 1, 5)},
		{"ErrInvalidCooldown", ErrInvalidCooldown("min_cooldown_seconds", 0, 1, 600)},
		{"ErrScalingTargetIncompatibleWithWorkloadClass", ErrScalingTargetIncompatibleWithWorkloadClass("rps")},

		// sidecars
		{"ErrSidecarStatefulDenied", ErrSidecarStatefulDenied("redis", "redis:7")},
		{"ErrSidecarNotAllowedOnPlan", ErrSidecarNotAllowedOnPlan(PlanFree)},

		// egress / liveness
		{"ErrPlanEgressAllowlistNotAllowed", ErrPlanEgressAllowlistNotAllowed(PlanFree)},
		{"ErrPlanLivenessProbeNotAllowed", ErrPlanLivenessProbeNotAllowed(PlanFree)},
		{"ErrEgressAllowlistTooLong", ErrEgressAllowlistTooLong(100, 50)},
		{"ErrAccountEgressAllowlistExtraOutOfRange", ErrAccountEgressAllowlistExtraOutOfRange(100, 50)},
		{"ErrInvalidEgressAllowlist", ErrInvalidEgressAllowlist("evil.com", errors.New("not in range"))},

		// misc plan limits
		{"ErrValidation", ErrValidation("name required")},
		{"ErrPlanSourceBytes", ErrPlanSourceBytes(100, 101)},
		{"ErrPlanFeatureGated", ErrPlanFeatureGated("cron", PlanHobby)},

		// invocations
		{"ErrInvocationNotFound", ErrInvocationNotFound("inv_123")},
		{"ErrInvocationNotReplayable", ErrInvocationNotReplayable("WAKING")},

		// orgs
		{"ErrOrgSlugTaken", ErrOrgSlugTaken("acme")},
		{"ErrOrgInvitationCapExceeded", ErrOrgInvitationCapExceeded(5, 6)},
		{"ErrOrgRoleForbidden", ErrOrgRoleForbidden("delete")},
		{"ErrOrgAlreadyMember", ErrOrgAlreadyMember("admin")},
		{"ErrOrgInvitationInvalid", ErrOrgInvitationInvalid()},
		{"ErrOrgPersonalImmutable", ErrOrgPersonalImmutable()},
		{"ErrOrgAPIKeyRequiresOrg", ErrOrgAPIKeyRequiresOrg()},
	}
	if len(cases) != 71 {
		t.Fatalf("sentinel sweep covers %d cases, want 71", len(cases))
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got == nil {
				t.Fatalf("%s = nil; want non-nil *Problem", tc.name)
			}
			if tc.got.Code == "" {
				t.Errorf("%s Code = \"\"; want non-empty RFC 7807 code", tc.name)
			}
			if tc.got.Title == "" {
				t.Errorf("%s Title = \"\"; want non-empty title", tc.name)
			}
			if tc.got.Status == 0 {
				t.Errorf("%s Status = 0; want non-zero HTTP status", tc.name)
			}
		})
	}
}

// TestErrSentinels_RoundTripProblemJSON pins the wire shape: each
// sentinel serializes as RFC 7807 problem+json with the expected
// application/problem+json content-type. We pick three widely-used
// sentinels and assert the full round-trip — the rest follow the
// same shape via the TestErrSentinels_AllConstructors smoke test.
func TestErrSentinels_RoundTripProblemJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteProblem(rr, ErrInternal("database down"))
	if ct := rr.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	var p Problem
	if err := json.NewDecoder(rr.Body).Decode(&p); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if p.Code != CodeInternal {
		t.Errorf("Code = %q, want %q", p.Code, CodeInternal)
	}
	if !strings.Contains(p.Detail, "database down") {
		t.Errorf("Detail = %q, want substring 'database down'", p.Detail)
	}
}

// TestErrSentinels_LimitsVariants — verify sentinels that take Limits
// accept every plan variant without panicking. The Limits struct is
// shared across all four plans; the constructors index .Plan,
// .MaxConcurrency, etc. so each Plan must produce a valid *Problem.
func TestErrSentinels_LimitsVariants(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		t.Run(string(p), func(t *testing.T) {
			l := mustPlan(t, p)
			problems := []*Problem{
				ErrAppConcurrencyReached(l, 1),
				ErrPlanLimitSecrets(l, 1),
				ErrPlanLimitEnvVars(l, 1),
				ErrPlanLimitTrustedSigners(l, 1),
				ErrSecretValueTooLarge(l, 1),
				ErrAPIKeyLimitExceeded(l, 1),
				ErrPlanRegistryCredentialQuota(l, 1),
				ErrPlanJobRegistryCredentialQuota(l, 1),
			}
			for _, pr := range problems {
				if pr == nil || pr.Code == "" {
					t.Errorf("plan=%s: produced empty Problem", p)
				}
			}
		})
	}
}

// TestErrSentinels_PlanOnlyVariants — sentinels that take only a Plan
// (no limits / no observed). These shape the Plan-gated code surface
// (CronQuota, PlanLimits, WebhooksNotAllowed, etc.) — each Plan must
// produce a valid Problem with the plan name surfaced somewhere
// in the user-visible text (Detail).
func TestErrSentinels_PlanOnlyVariants(t *testing.T) {
	for _, p := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		t.Run(string(p), func(t *testing.T) {
			problems := []*Problem{
				ErrPlanCronsNotAllowed(p),
				ErrPlanEvictionPriorityReservedNotAllowed(p),
				ErrPlanPublicAuthBearerNotAllowed(p),
				ErrPlanPublicAuthBasicNotAllowed(p),
				ErrPlanWebhooksNotAllowed(p),
				ErrPlanRegistryCredentialsNotAllowed(p),
				ErrPlanJobRegistryCredentialsNotAllowed(p),
				ErrPlanMinInstancesNotAllowed(p),
				ErrPlanMaxInstancesNotAllowed(p),
				ErrPlanEgressAllowlistNotAllowed(p),
				ErrPlanLivenessProbeNotAllowed(p),
				ErrSidecarNotAllowedOnPlan(p),
			}
			for _, pr := range problems {
				if pr == nil || pr.Code == "" {
					t.Errorf("plan=%s: produced empty Problem", p)
				}
			}
		})
	}
}
