package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// Organization paths disambiguate the same project slug in different companies.
// The existing handlers retain their scope, verification and replay middleware.
func (s *server) registerOrganizationProjectRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/orgs/{organization}/projects", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listOrgProjects))))
	mux.HandleFunc("GET /v1/orgs/{organization}/projects/{slug}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getProject))))
	mux.HandleFunc("GET /v1/orgs/{organization}/projects/{slug}/environments", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listProjectEnvironments))))
	mux.HandleFunc("GET /v1/orgs/{organization}/projects/{slug}/environments/{environment}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getProjectEnvironment))))
	mux.HandleFunc("GET /v1/orgs/{organization}/projects/{slug}/environments/{environment}/releases", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getProjectEnvironmentReleases))))
	mux.HandleFunc("GET /v1/orgs/{organization}/projects/{slug}/environments/{environment}/release-sets", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listProjectReleaseSets))))
	mux.HandleFunc("GET /v1/orgs/{organization}/projects/{slug}/environments/{environment}/release-sets/active", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getActiveProjectReleaseSet))))
	mux.HandleFunc("GET /v1/orgs/{organization}/projects/{slug}/environments/{environment}/release-sets/{release}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getProjectReleaseSet))))
	mux.HandleFunc("GET /v1/orgs/{organization}/projects/{slug}/environments/{environment}/state", s.authLimited(s.requireMFA(s.requireScope(api.ScopesProjectEnvironmentReadSurface...)(s.getProjectEnvironmentState))))
	mux.HandleFunc("GET /v1/orgs/{organization}/projects/{slug}/environments/{environment}/diff", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.diffProjectEnvironment))))
	mux.HandleFunc("GET /v1/orgs/{organization}/projects/{slug}/environments/{environment}/config", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getProjectEnvironmentConfig))))
	mux.HandleFunc("GET /v1/orgs/{organization}/projects/{slug}/environments/{environment}/config/diff", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.diffProjectEnvironmentConfig))))
	mux.HandleFunc("GET /v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotion-preview", s.authLimited(s.requireMFA(s.requireScope(api.ScopesProjectEnvironmentReadSurface...)(s.previewProjectEnvironmentPromotion))))
	mux.HandleFunc("GET /v1/orgs/{organization}/projects/{slug}/environments/{environment}/approvals/{approval}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getProjectEnvironmentApprovalStatus))))
	mux.HandleFunc("GET /v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotions/{promotion}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getProjectEnvironmentPromotionStatus))))
	mux.HandleFunc("GET /v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotions", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listProjectEnvironmentPromotions))))
	mux.HandleFunc("GET /v1/orgs/{organization}/projects/{slug}/delete-preview", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.previewDeleteProject))))
	mux.HandleFunc("POST /v1/orgs/{organization}/projects/{slug}/environments", s.authVerifiedLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.requireVerifiedEmail(s.idempotent(s.createProjectEnvironment))))))
	mux.HandleFunc("PUT /v1/orgs/{organization}/projects/{slug}/environments/{environment}/config", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.updateProjectEnvironmentConfig)))))
	mux.HandleFunc("PUT /v1/orgs/{organization}/projects/{slug}/environments/{environment}/workloads/{workload}/routes", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.updateProjectEnvironmentRoutes)))))
	mux.HandleFunc("DELETE /v1/orgs/{organization}/projects/{slug}/exclusions/{slug2}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteDeploymentScopeExclusion))))
	mux.HandleFunc("POST /v1/orgs/{organization}/projects", s.authVerifiedLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.requireVerifiedEmail(s.idempotentIntent(s.applyProject))))))
	mux.HandleFunc("POST /v1/orgs/{organization}/projects/scan", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.scanProject))))
	mux.HandleFunc("POST /v1/orgs/{organization}/projects/{slug}/environments/{environment}/qualifications", s.authLimited(s.requireMFA(s.requireScope(api.ScopesProjectEnvironmentQualifySurface...)(s.idempotent(s.createProjectEnvironmentQualification)))))
	mux.HandleFunc("POST /v1/orgs/{organization}/projects/{slug}/environments/{environment}/release-sets", s.authVerifiedLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.requireVerifiedEmail(s.idempotent(s.publishProjectReleaseSet))))))
	mux.HandleFunc("POST /v1/orgs/{organization}/projects/{slug}/environments/{environment}/promote", s.authVerifiedLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.requireVerifiedEmail(s.idempotent(s.promoteProjectEnvironment))))))
	mux.HandleFunc("POST /v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotions/{promotion}/rollback", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.rollbackProjectEnvironmentPromotion)))))
	// A company promotion that can be started and rolled back can also be
	// stopped or recovered, have its migration recovered, and be previewed with
	// release secrets; these mirror the legacy routes' wrappers.
	mux.HandleFunc("POST /v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotions/{promotion}/recover-migration", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.recoverProjectPromotionMigration)))))
	mux.HandleFunc("POST /v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotions/{promotion}/{action}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.controlProjectPromotion)))))
	mux.HandleFunc("POST /v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotion-preview", s.authLimited(s.requireMFA(s.requireScope(api.ScopesProjectEnvironmentReadSurface...)(s.previewProjectEnvironmentPromotionSecrets))))
	mux.HandleFunc("PATCH /v1/orgs/{organization}/projects/{slug}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.updateProject))))
	mux.HandleFunc("PATCH /v1/orgs/{organization}/projects/{slug}/environments/{environment}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.updateProjectEnvironment))))
	mux.HandleFunc("PUT /v1/orgs/{organization}/projects/{slug}/environments/{environment}/workloads/{workload}/policies", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.updateProjectEnvironmentPolicies)))))
	mux.HandleFunc("POST /v1/orgs/{organization}/projects/{slug}/environments/{environment}/approvals", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.approveProjectEnvironment)))))
	mux.HandleFunc("DELETE /v1/orgs/{organization}/projects/{slug}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteProject))))
	mux.HandleFunc("DELETE /v1/orgs/{organization}/projects/{slug}/environments/{environment}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.deleteProjectEnvironment)))))
	mux.HandleFunc("POST /v1/orgs/{organization}/projects/{slug}/scan", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.scanProject))))
	mux.HandleFunc("POST /v1/orgs/{organization}/projects/{slug}/deploy", s.authVerifiedLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.requireVerifiedEmail(s.idempotentIntent(s.applyProject))))))
}
