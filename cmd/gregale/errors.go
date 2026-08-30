package main

import (
	"github.com/onebox-faas/faas/pkg/api"
)

// docsURLPrefix is the valid public fallback used when the API omits a
// Problem.DocsURL. The web app currently serves a consolidated CLI reference
// rather than the former per-error docs host.
var docsURLPrefix = cliDocsURL

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
	api.CodePlanLimitApps:              cliDocsURL,
	api.CodePlanLimitRAM:               cliDocsURL,
	api.CodePlanLimitConcur:            cliDocsURL,
	api.CodeSourceTooLarge:             deployFromSourceDocsURL,
	api.CodeSourceInvalid:              deployFromSourceDocsURL,
	api.CodeSecretScanStrict:           cliDocsURL,
	api.CodeAppLayerTooBig:             deployFromSourceDocsURL,
	api.CodeBuildUndetected:            deployFromSourceDocsURL,
	api.CodeBuildOOM:                   deployFromSourceDocsURL,
	api.CodeBuildTimeout:               deployFromSourceDocsURL,
	api.CodeQuotaExhausted:             cliDocsURL,
	api.CodeBillingPastDue:             cliDocsURL,
	api.CodeCapacity:                   cliDocsURL,
	api.CodeUnauthorized:               cliDocsURL,
	api.CodePasswordTooWeak:            cliDocsURL,
	api.CodeInvalidCredentials:         cliDocsURL,
	api.CodeNotFound:                   cliDocsURL,
	api.CodeValidation:                 cliDocsURL,
	api.CodeConflict:                   cliDocsURL,
	api.CodeDomainNotVerified:          cliDocsURL,
	api.CodeCronInvalid:                cliDocsURL,
	api.CodeHandlerMissing:             cliDocsURL,
	api.CodeImageRequired:              cliDocsURL,
	api.CodeDeployFailed:               cliDocsURL,
	api.CodeNoRollbackTarget:           cliDocsURL,
	api.CodePlanLimitSecrets:           cliDocsURL,
	api.CodeSecretInvalidKey:           cliDocsURL,
	api.CodeSecretValueTooLarge:        cliDocsURL,
	api.CodeSecretNotFound:             cliDocsURL,
	api.CodePlanMinInstancesNotAllowed: cliDocsURL,
	api.CodeInvalidMinInstances:        cliDocsURL,
	api.CodePlanTrafficSplitNotAllowed: cliDocsURL,
	api.CodeInvalidTrafficPercent:      cliDocsURL,
	api.CodeTrafficPercentSumInvalid:   cliDocsURL,
	api.CodeAccountDeletionConfirm:     cliDocsURL,
	api.CodeAccountDeletionPending:     cliDocsURL,
	api.CodeAccountNotRestorable:       cliDocsURL,
	api.CodeAppRenameFailed:            cliDocsURL,
	api.CodeCliAuthPending:             cliDocsURL,
	api.CodeCliAuthUnavailable:         cliDocsURL,
	api.CodeAppNotListening:            cliDocsURL,
	api.CodeAppLoopbackBound:           cliDocsURL,
	api.CodeAppArchMismatch:            cliDocsURL,
	api.CodeEnvVarMissing:              cliDocsURL,
	api.CodeAppHealthzUnauthorized:     cliDocsURL,
	api.CodeAppRuntimeOOM:              cliDocsURL,
	api.CodeDepInstallFailed:           cliDocsURL,
	api.CodeAppStartupTimeout:          cliDocsURL,
}

// docsURLForCode returns a valid docs route for a stable code, falling back to
// the generic CLI reference when the code has no entry (or is empty). Used
// by APIError.Error to synthesise the third line when the server omits
// Problem.DocsURL, so spec §3.3's three-line shape always holds.
func docsURLForCode(code string) string {
	if u, ok := errorDocsURL[code]; ok {
		return u
	}
	return docsURLPrefix
}
