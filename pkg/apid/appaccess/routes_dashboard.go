package appaccess

import "github.com/onebox-faas/faas/pkg/authz"

// dashboardRouteActions reviews the session-only dashboard writes for apps
// that routeActions did not list (bug hunt 37), by featureRouteActions'
// rules. An unlisted dashboard write fell back to the app's creator alone, as
// an unlisted /v1 endpoint did. TestEveryAppRouteIsReviewed covers them.
var dashboardRouteActions = map[string]authz.OrgAction{
	"POST /dashboard/apps/{slug}/alert-presets/{name}/enable":                      authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/alert-presets/{name}/test":                        authz.OrgActionDeployApp,
	"POST /dashboard/apps/{slug}/auth/keys":                                        authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/auth/keys/{id}/activate":                          authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/auth/keys/{id}/revoke":                            authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/auth/providers":                                   authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/auth/providers/{provider}/disable":                authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/auth/settings":                                    authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/automations/live-suites/{suite_id}/advance":       authz.OrgActionDeployApp,
	"POST /dashboard/apps/{slug}/automations/live-verifications/{run_id}":          authz.OrgActionDeployApp,
	"POST /dashboard/apps/{slug}/automations/{action}":                             authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/crons/{id}/fire-now":                              authz.OrgActionDeployApp,
	"POST /dashboard/apps/{slug}/crons/{id}/policy":                                authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/data-api/refresh":                                 authz.OrgActionDeployApp,
	"POST /dashboard/apps/{slug}/data-api/{operation}":                             authz.OrgActionDeployApp,
	"POST /dashboard/apps/{slug}/debug/requests/{req_id}/replay":                   authz.OrgActionDeployApp,
	"POST /dashboard/apps/{slug}/github/activity/retry":                            authz.OrgActionDeployApp,
	"POST /dashboard/apps/{slug}/github/disconnect":                                authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/github/sync":                                      authz.OrgActionDeployApp,
	"POST /dashboard/apps/{slug}/issues/bulk-actions":                              authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/issues/compound-alerts":                           authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/issues/frequency-alerts":                          authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/issues/impact-alert-policy":                       authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/issues/lifecycle-alerts":                          authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/issues/ownership-rules":                           authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/issues/saved-views":                               authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/issues/teams":                                     authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/issues/tracker-notifications":                     authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/issues/{issue_id}/actions":                        authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/issues/{issue_id}/linear-connection":              authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/issues/{issue_id}/split-rules":                    authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/issues/{issue_id}/split-rules/{rule_id}":          authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/issues/{issue_id}/split-rules/{rule_id}/undo":     authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/issues/{issue_id}/tracker-links":                  authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/issues/{issue_id}/tracker-sync":                   authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/mirrors":                                          authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/mirrors/{id}/delete":                              authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/mirrors/{id}/toggle":                              authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/preview/{previewSlug}/destroy":                    authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/product-control/access/confirm":                   authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/product-control/access/review":                    authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/product-control/confirm":                          authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/product-control/email/{delivery}/retry":           authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/product-control/help/{request}":                   authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/product-control/onboarding/confirm":               authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/product-control/onboarding/disconnect":            authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/product-control/onboarding/review":                authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/product-control/onboarding/session":               authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/product-control/plan-policy/confirm":              authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/product-control/plan-policy/review":               authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/product-control/review":                           authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/product-control/setup/confirm":                    authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/product-control/setup/review":                     authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/product-control/team-policy/confirm":              authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/product-control/team-policy/review":               authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/profiles/captures":                                authz.OrgActionDeployApp,
	"POST /dashboard/apps/{slug}/profiles/captures/{id}/delete":                    authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/profiles/deployment-policy":                       authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/profiles/investigations":                          authz.OrgActionDeployApp,
	"POST /dashboard/apps/{slug}/profiles/investigations/{id}/check":               authz.OrgActionDeployApp,
	"POST /dashboard/apps/{slug}/routes/insights/{id}/fix":                         authz.OrgActionConfigureApp,
	"POST /dashboard/apps/{slug}/storage/objects/delete":                           authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/storage/signed-url":                               authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/tenant-surfaces":                                  authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/tenant-surfaces/{id}/delete":                      authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/tenant-surfaces/{id}/hostnames":                   authz.OrgActionManageAppSecurity,
	"POST /dashboard/apps/{slug}/tenant-surfaces/{id}/hostnames/{hostname}/delete": authz.OrgActionManageAppSecurity,
}

func init() {
	for pattern, action := range dashboardRouteActions {
		if _, dup := routeActions[pattern]; dup {
			panic("appaccess: " + pattern + " is reviewed twice")
		}
		routeActions[pattern] = action
	}
}
