// Package appaccess authorizes shared application operations without replacing
// the authenticated principal with the account that currently pays for the app.
package appaccess

import "github.com/onebox-faas/faas/pkg/authz"

// Reviewed routes are explicit: adding a new app endpoint never implicitly
// grants organization members access to a creator's other resources.
var routeActions = map[string]authz.OrgAction{
	// The resumable upload routes name their app in the request body or the
	// stored session, so the handlers call Authorize once they resolve it.
	"POST /v1/uploads":                                authz.OrgActionDeployApp,
	"POST /v1/uploads/{id}/commit":                    authz.OrgActionDeployApp,
	"GET /v1/apps/{slug}":                             authz.OrgActionReadApp,
	"PATCH /v1/apps/{slug}":                           authz.OrgActionConfigureApp,
	"DELETE /v1/apps/{slug}":                          authz.OrgActionDeleteApp,
	"POST /v1/apps/{slug}/restore":                    authz.OrgActionDeleteApp,
	"POST /v1/apps/{slug}/park":                       authz.OrgActionDeployApp,
	"POST /v1/apps/{slug}/park/conditional":           authz.OrgActionDeployApp,
	"POST /v1/apps/{slug}/wake":                       authz.OrgActionDeployApp,
	"GET /v1/apps/{slug}/env":                         authz.OrgActionReadApp,
	"PUT /v1/apps/{slug}/env/{key}":                   authz.OrgActionConfigureApp,
	"DELETE /v1/apps/{slug}/env/{key}":                authz.OrgActionConfigureApp,
	"GET /v1/apps/{slug}/env-diff":                    authz.OrgActionReadApp,
	"GET /v1/apps/{slug}/secrets":                     authz.OrgActionReadApp,
	"PUT /v1/apps/{slug}/secrets/{key}":               authz.OrgActionManageAppSecrets,
	"DELETE /v1/apps/{slug}/secrets/{key}":            authz.OrgActionManageAppSecrets,
	"POST /v1/apps/{slug}/secrets/{key}/rotate":       authz.OrgActionManageAppSecrets,
	"GET /v1/apps/{slug}/security":                    authz.OrgActionReadApp,
	"PATCH /v1/apps/{slug}/security":                  authz.OrgActionManageAppSecurity,
	"POST /v1/apps/{slug}/security/recover":           authz.OrgActionDeployApp,
	"GET /v1/apps/{slug}/trusted_signers":             authz.OrgActionManageAppSecurity,
	"PUT /v1/apps/{slug}/trusted_signers/{name}":      authz.OrgActionManageAppSecurity,
	"DELETE /v1/apps/{slug}/trusted_signers/{name}":   authz.OrgActionManageAppSecurity,
	"GET /v1/apps/{slug}/deployments":                 authz.OrgActionReadApp,
	"GET /v1/apps/{slug}/deployments/latest":          authz.OrgActionReadApp,
	"GET /v1/apps/{slug}/deployments/{id}/summary":    authz.OrgActionReadApp,
	"POST /v1/apps/{slug}/deployments":                authz.OrgActionDeployApp,
	"POST /v1/apps/{slug}/deployments/{id}/cancel":    authz.OrgActionDeployApp,
	"POST /v1/apps/{slug}/deployments/clear-obsolete": authz.OrgActionDeployApp,
	"GET /v1/deployments/{id}":                        authz.OrgActionReadApp,
	"GET /v1/deployments/{id}/logs":                   authz.OrgActionReadAppLogs,
	"GET /v1/deployments/{id}/stages":                 authz.OrgActionReadApp,
	"GET /v1/deployments/{id}/scan":                   authz.OrgActionReadApp,
	"GET /v1/deployments/{id}/secret-scan":            authz.OrgActionReadApp,
	"GET /v1/deployments/{id}/audit":                  authz.OrgActionReadApp,
	"PATCH /v1/deployments/{id}":                      authz.OrgActionConfigureApp,
	"POST /v1/deployments/{id}/retry":                 authz.OrgActionDeployApp,
	"GET /v1/builds/{id}":                             authz.OrgActionReadApp,
	"GET /v1/builds/{id}/provenance":                  authz.OrgActionReadApp,
	"GET /v1/builds/{id}/sbom":                        authz.OrgActionReadApp,
	"GET /v1/apps/{slug}/logs":                        authz.OrgActionReadAppLogs,

	// Release approvals (ADR-20261020085706): only owners and admins may
	// change the policy, so a developer cannot switch approvals off.
	"GET /v1/apps/{slug}/release-approval-policy":    authz.OrgActionReadApp,
	"PUT /v1/apps/{slug}/release-approval-policy":    authz.OrgActionManageAppSecurity,
	"DELETE /v1/apps/{slug}/release-approval-policy": authz.OrgActionManageAppSecurity,
	"GET /v1/apps/{slug}/release-approvals":          authz.OrgActionReadApp,
	"GET /v1/deployments/{id}/approvals":             authz.OrgActionReadApp,
	"POST /v1/deployments/{id}/approvals":            authz.OrgActionApproveAppRelease,
	// Release diff (ADR-20261021101117).
	"GET /v1/deployments/{id}/diff": authz.OrgActionReadApp,
	// Scheduled releases (ADR-20261021152201): scheduling a release is
	// deploying it.
	"GET /v1/deployments/{id}/schedule":    authz.OrgActionReadApp,
	"PUT /v1/deployments/{id}/schedule":    authz.OrgActionDeployApp,
	"DELETE /v1/deployments/{id}/schedule": authz.OrgActionDeployApp,

	// Durable workflows (Hunt 13 H13-2). Viewers read runs; developers start,
	// steer and replay them, as they deploy the code the runs execute.
	// Binding a callback's webhook verification secret manages a secret. Run
	// URLs name no app, so indirectlyAddressedApp resolves the run's app.
	"POST /v1/apps/{slug}/workflows/{name}/runs":                             authz.OrgActionDeployApp,
	"GET /v1/apps/{slug}/workflows/runs":                                     authz.OrgActionReadApp,
	"POST /v1/apps/{slug}/workflows/runs:cancel-preview":                     authz.OrgActionReadApp,
	"POST /v1/apps/{slug}/workflows/runs:cancel-queued":                      authz.OrgActionDeployApp,
	"GET /v1/apps/{slug}/workflows/schedules":                                authz.OrgActionReadApp,
	"GET /v1/apps/{slug}/workflows/schedules/{name}/preview":                 authz.OrgActionReadApp,
	"GET /v1/apps/{slug}/workflows/schedules/occurrences":                    authz.OrgActionReadApp,
	"POST /v1/apps/{slug}/workflows/schedules/occurrences:replay-preview":    authz.OrgActionReadApp,
	"POST /v1/apps/{slug}/workflows/schedules/occurrences:replay":            authz.OrgActionDeployApp,
	"GET /v1/workflows/runs/{id}":                                            authz.OrgActionReadApp,
	"GET /v1/workflows/runs/{id}/steps":                                      authz.OrgActionReadApp,
	"GET /v1/workflows/runs/{id}/steps/{step}/attempts":                      authz.OrgActionReadApp,
	"GET /v1/workflows/runs/{id}/diagnostics":                                authz.OrgActionReadApp,
	"GET /v1/workflows/runs/{id}/resumes":                                    authz.OrgActionReadApp,
	"GET /v1/workflows/runs/{id}/callbacks":                                  authz.OrgActionReadApp,
	"GET /v1/workflows/runs/{id}/callbacks/{callback_id}/webhook-binding":    authz.OrgActionReadApp,
	"POST /v1/workflows/runs/{id}/cancel":                                    authz.OrgActionDeployApp,
	"POST /v1/workflows/runs/{id}/resume":                                    authz.OrgActionDeployApp,
	"POST /v1/workflows/runs/{id}/events":                                    authz.OrgActionDeployApp,
	"POST /v1/workflows/runs/{id}/steps/{step}/retry":                        authz.OrgActionDeployApp,
	"POST /v1/workflows/runs/{id}/callbacks/{callback_id}":                   authz.OrgActionDeployApp,
	"PUT /v1/workflows/runs/{id}/callbacks/{callback_id}/webhook-binding":    authz.OrgActionManageAppSecrets,
	"DELETE /v1/workflows/runs/{id}/callbacks/{callback_id}/webhook-binding": authz.OrgActionManageAppSecrets,
}
