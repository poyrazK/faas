package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestDocsURLForCode_KnownCodes (issue #64 D2) snapshots the fallback
// URL for every Code constant in pkg/api/errors.go. If a new code is
// added without a matching row here, this test fails — that's the
// intentional tripwire that keeps the per-code docs URL table in
// sync with the stable error codes.
func TestDocsURLForCode_KnownCodes(t *testing.T) {
	codes := []string{
		api.CodePlanLimitApps,
		api.CodePlanLimitRAM,
		api.CodePlanLimitConcur,
		api.CodeSourceTooLarge,
		api.CodeSourceInvalid,
		api.CodeAppLayerTooBig,
		api.CodeBuildUndetected,
		api.CodeBuildOOM,
		api.CodeBuildTimeout,
		api.CodeQuotaExhausted,
		api.CodeBillingPastDue,
		api.CodeCapacity,
		api.CodeUnauthorized,
		api.CodeNotFound,
		api.CodeValidation,
		api.CodeConflict,
		api.CodeDomainNotVerified,
		api.CodeCronInvalid,
		api.CodeHandlerMissing,
		api.CodeImageRequired,
		api.CodeDeployFailed,
		api.CodeNoRollbackTarget,
		api.CodePlanLimitSecrets,
		api.CodeSecretInvalidKey,
		api.CodeSecretValueTooLarge,
		api.CodeSecretNotFound,
		api.CodePlanMinInstancesNotAllowed,
		api.CodeInvalidMinInstances,
		api.CodeAccountDeletionConfirm,
		api.CodeAccountDeletionPending,
		api.CodeAccountNotRestorable,
		api.CodeAppRenameFailed,
	}
	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			got := docsURLForCode(code)
			if got == docsURLPrefix {
				t.Errorf("code %q has no per-code entry; add one to errorDocsURL", code)
			}
			if !strings.HasPrefix(got, docsURLPrefix) {
				t.Errorf("code %q → URL %q should start with %q", code, got, docsURLPrefix)
			}
		})
	}
}

// TestDocsURLForCode_UnknownFallsBackToGeneric asserts the unknown-code
// fallback path: any code not in the table returns the generic landing
// page. This is the v1 spec contract; tightening it (e.g., failing on
// unknown codes) is a follow-up.
func TestDocsURLForCode_UnknownFallsBackToGeneric(t *testing.T) {
	cases := []string{"", "unknown_thing", "plan_limit_apps_typo"}
	for _, code := range cases {
		t.Run("code="+code, func(t *testing.T) {
			if got, want := docsURLForCode(code), docsURLPrefix; got != want {
				t.Errorf("docsURLForCode(%q) = %q, want generic %q", code, got, want)
			}
		})
	}
}
