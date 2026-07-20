package main

import "github.com/onebox-faas/faas/pkg/api"

// docsURLPrefix is the placeholder docs host used to synthesise the
// "→ see docs at …" line when Problem.DocsURL is empty. The constant
// is the single point of override (an env-var override is a follow-up).
// faas.example is reserved by IANA (RFC 2606) and unreachable, so a stray
// lookup fails fast and is obviously wrong.
const docsURLPrefix = "https://docs.faas.example/errors"

// errorDocsURL is the per-stable-Code docs URL table. Codes live in
// pkg/api/errors.go; this table mirrors them 1:1 and is consulted only
// when the server-side Problem.DocsURL is empty (which happens for
// codes the constructor chain doesn't decorate with WithDocs, or for
// problems reconstructed from a bare gRPC status — see
// pkg/api/errors.go::StatusForCode).
//
// Adding a new code in pkg/api/errors.go → add a matching row here.
// When adding, follow the existing path-style convention (lower-kebab).
var errorDocsURL = map[string]string{
	api.CodePlanLimitApps:              docsURLPrefix + "/plan-limit-apps",
	api.CodePlanLimitRAM:               docsURLPrefix + "/plan-limit-ram",
	api.CodePlanLimitConcur:            docsURLPrefix + "/plan-limit-concurrency",
	api.CodeSourceTooLarge:             docsURLPrefix + "/build/source#size",
	api.CodeSourceInvalid:              docsURLPrefix + "/build/source",
	api.CodeAppLayerTooBig:             docsURLPrefix + "/build/limits#app-layer",
	api.CodeBuildUndetected:            docsURLPrefix + "/build/detect",
	api.CodeBuildOOM:                   docsURLPrefix + "/build/limits#memory",
	api.CodeBuildTimeout:               docsURLPrefix + "/build/limits#timeout",
	api.CodeQuotaExhausted:             docsURLPrefix + "/quota",
	api.CodeBillingPastDue:             docsURLPrefix + "/billing",
	api.CodeCapacity:                   docsURLPrefix + "/capacity",
	api.CodeUnauthorized:               docsURLPrefix + "/auth",
	api.CodeNotFound:                   docsURLPrefix + "/not-found",
	api.CodeValidation:                 docsURLPrefix + "/validation",
	api.CodeConflict:                   docsURLPrefix + "/conflict",
	api.CodeDomainNotVerified:          docsURLPrefix + "/domains/verify",
	api.CodeCronInvalid:                docsURLPrefix + "/crons",
	api.CodeHandlerMissing:             docsURLPrefix + "/functions",
	api.CodeImageRequired:              docsURLPrefix + "/deploys",
	api.CodeDeployFailed:               docsURLPrefix + "/deploys",
	api.CodeNoRollbackTarget:           docsURLPrefix + "/deploys/rollback",
	api.CodePlanLimitSecrets:           docsURLPrefix + "/secrets/limits",
	api.CodeSecretInvalidKey:           docsURLPrefix + "/secrets/keys",
	api.CodeSecretValueTooLarge:        docsURLPrefix + "/secrets/limits",
	api.CodeSecretNotFound:             docsURLPrefix + "/secrets",
	api.CodePlanMinInstancesNotAllowed: docsURLPrefix + "/plans/min-instances",
	api.CodeInvalidMinInstances:        docsURLPrefix + "/apps/min-instances",
	api.CodeAccountDeletionConfirm:     docsURLPrefix + "/account/delete",
	api.CodeAccountDeletionPending:     docsURLPrefix + "/account/delete",
	api.CodeAccountNotRestorable:       docsURLPrefix + "/account/delete",
	api.CodeAppRenameFailed:            docsURLPrefix + "/apps/rename",
}

// docsURLForCode returns the per-code docs URL, falling back to the
// generic landing page when the code has no entry (or is empty). Used
// by APIError.Error to synthesise the third line when the server omits
// Problem.DocsURL, so spec §3.3's three-line shape always holds.
func docsURLForCode(code string) string {
	if u, ok := errorDocsURL[code]; ok {
		return u
	}
	return docsURLPrefix
}
