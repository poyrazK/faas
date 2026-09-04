package faas

import "github.com/poyrazK/faas-go/internal/api"

// Problem.Code values. The string values are part of the wire
// contract; the SDK re-exports the typed constants so customers
// can write `if ae.Code == faas.CodeNotFound { ... }` without
// stringly-typed risk.
//
// The full set is mirrored at the DB layer by migration 00044's
// api_keys_scopes_vocab_chk CHECK constraint on a separate column;
// the Problem.Code itself is not DB-constrained, so a new code
// can be added server-side without migration. Adding a sentinel
// (errors.go) is the one piece that requires an SDK release.
const (
	CodePlanLimitApps                 = api.CodePlanLimitApps
	CodePlanLimitRAM                  = api.CodePlanLimitRAM
	CodePlanLimitConcur               = api.CodePlanLimitConcur
	CodeSourceTooLarge                = api.CodeSourceTooLarge
	CodeSourceInvalid                 = api.CodeSourceInvalid
	CodeAppLayerTooBig                = api.CodeAppLayerTooBig
	CodeBuildUndetected               = api.CodeBuildUndetected
	CodeBuildOOM                      = api.CodeBuildOOM
	CodeBuildTimeout                  = api.CodeBuildTimeout
	CodeQuotaExhausted                = api.CodeQuotaExhausted
	CodeBillingPastDue                = api.CodeBillingPastDue
	CodeCapacity                      = api.CodeCapacity
	CodeUnauthorized                  = api.CodeUnauthorized
	CodeMFARequired                   = api.CodeMFARequired
	CodeStepUpRequired                = api.CodeStepUpRequired
	CodeUnsupportedByCLI              = api.CodeUnsupportedByCLI
	CodeForbidden                     = api.CodeForbidden
	CodeNotFound                      = api.CodeNotFound
	CodeValidation                    = api.CodeValidation
	CodeConflict                      = api.CodeConflict
	CodeDomainNotVerified             = api.CodeDomainNotVerified
	CodeCronInvalid                   = api.CodeCronInvalid
	CodeHandlerMissing                = api.CodeHandlerMissing
	CodeImageRequired                 = api.CodeImageRequired
	CodeDeployFailed                  = api.CodeDeployFailed
	CodeNoRollbackTarget              = api.CodeNoRollbackTarget
	CodePayment                       = api.CodePayment
	CodePlanLimitSecrets              = api.CodePlanLimitSecrets
	CodeSecretInvalidKey              = api.CodeSecretInvalidKey
	CodeSecretValueTooLarge           = api.CodeSecretValueTooLarge
	CodeSecretNotFound                = api.CodeSecretNotFound
	CodePlanMinInstancesNotAllowed    = api.CodePlanMinInstancesNotAllowed
	CodeInvalidMinInstances           = api.CodeInvalidMinInstances
	CodePlanEgressAllowlistNotAllowed = api.CodePlanEgressAllowlistNotAllowed
	CodeEgressAllowlistTooLong        = api.CodeEgressAllowlistTooLong
	CodePlanQueueDepth                = api.CodePlanQueueDepth
	CodePlanSourceBytes               = api.CodePlanSourceBytes
	CodePlanFeatureGated              = api.CodePlanFeatureGated
	CodePlanDelayedCap                = api.CodePlanDelayedCap
	CodeInvocationNotFound            = api.CodeInvocationNotFound
	CodeInvalidAutoscaleTargetRPS     = api.CodeInvalidAutoscaleTargetRPS
	CodeInvalidAutoscaleTargetCPU     = api.CodeInvalidAutoscaleTargetCPU
	CodeInvalidEgressAllowlist        = api.CodeInvalidEgressAllowlist
	CodeAccountDeletionConfirm        = api.CodeAccountDeletionConfirm
	CodeAccountDeletionPending        = api.CodeAccountDeletionPending
	CodeAccountNotRestorable          = api.CodeAccountNotRestorable
	CodeAppRenameFailed               = api.CodeAppRenameFailed
	CodeImageNotFound                 = api.CodeImageNotFound
	CodeImageEgressDenied             = api.CodeImageEgressDenied
	CodeImageManifestInvalid          = api.CodeImageManifestInvalid
	CodeCliAuthPending                = api.CodeCliAuthPending
	CodeCliAuthUnavailable            = api.CodeCliAuthUnavailable
	CodeAppConcurReached              = api.CodeAppConcurReached
	CodeInvalidCredentials            = api.CodeInvalidCredentials
	CodeEmailNotVerified              = api.CodeEmailNotVerified
	CodePasswordTooWeak               = api.CodePasswordTooWeak
	CodeResetTokenInvalid             = api.CodeResetTokenInvalid
	CodeResetTokenExpired             = api.CodeResetTokenExpired
	CodeAccountExists                 = api.CodeAccountExists
	CodeRateLimited                   = api.CodeRateLimited
)
