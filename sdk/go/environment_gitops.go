package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

// Environment GitOps wire contracts. Client methods are inherited from the
// embedded API client and retain the reviewed digest/generation/plan hash.
type (
	RebindEnvironmentGitSourceRequest      = api.RebindEnvironmentGitSourceRequest
	DetachEnvironmentGitSourceRequest      = api.DetachEnvironmentGitSourceRequest
	EnvironmentFieldOwnershipRequest       = api.EnvironmentFieldOwnershipRequest
	EnvironmentFieldOwnershipResponse      = api.EnvironmentFieldOwnershipResponse
	CreateEnvironmentGitSourceRequest      = api.CreateEnvironmentGitSourceRequest
	PreviewEnvironmentGitRevisionRequest   = api.PreviewEnvironmentGitRevisionRequest
	PreviewEnvironmentGitRevisionResponse  = api.PreviewEnvironmentGitRevisionResponse
	ApproveEnvironmentGitRevisionRequest   = api.ApproveEnvironmentGitRevisionRequest
	ApproveEnvironmentGitRevisionResponse  = api.ApproveEnvironmentGitRevisionResponse
	EnvironmentGitOpsStatusResponse        = api.EnvironmentGitOpsStatusResponse
	AdoptEnvironmentGitOpsRequest          = api.AdoptEnvironmentGitOpsRequest
	AdoptEnvironmentGitOpsResponse         = api.AdoptEnvironmentGitOpsResponse
	RemoveEnvironmentGitOpsOverrideRequest = api.RemoveEnvironmentGitOpsOverrideRequest
	EnvironmentGitSourceSpec               = api.EnvironmentGitSourceSpec
	EnvironmentGitSource                   = api.EnvironmentGitSource
	EnvironmentDesiredRevision             = api.EnvironmentDesiredRevision
	EnvironmentGitOpsRun                   = api.EnvironmentGitOpsRun
	EnvironmentGitSourceUpdate             = api.EnvironmentGitSourceUpdate
	EnvironmentGitOpsOverrideRequest       = api.EnvironmentGitOpsOverrideRequest
	EnvironmentGitOpsChange                = api.EnvironmentGitOpsChange
	EnvironmentGitOpsPlan                  = api.EnvironmentGitOpsPlan
	EnvironmentDefinition                  = api.EnvironmentDefinition
	EnvironmentWorkload                    = api.EnvironmentWorkload
	EnvironmentWorkloadSource              = api.EnvironmentWorkloadSource
	EnvironmentRouteContract               = api.EnvironmentRouteContract
	EnvironmentPolicy                      = api.EnvironmentPolicy
	EnvironmentQueueBinding                = api.EnvironmentQueueBinding
	EnvironmentServiceBinding              = api.EnvironmentServiceBinding
)
