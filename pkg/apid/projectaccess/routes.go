package projectaccess

import (
	"strings"

	"github.com/onebox-faas/faas/pkg/authz"
)

// Routes are individually reviewed. Unlisted GitOps, data-service, full clone
// and browser-approval operations remain outside shared project administration.
var routeActions = map[string]authz.OrgAction{
	"GET /v1/projects/{slug}/environments/{environment}/workloads/{workload}/queue-bindings": authz.OrgActionReadQueues,
	"PUT /v1/projects/{slug}/environments/{environment}/workloads/{workload}/queue-bindings": authz.OrgActionConfigureQueues,
	"GET /v1/projects":                                                               authz.OrgActionReadProject,
	"GET /v1/projects/{slug}":                                                        authz.OrgActionReadProject,
	"GET /v1/projects/{slug}/environments":                                           authz.OrgActionReadProject,
	"GET /v1/projects/{slug}/environments/{environment}":                             authz.OrgActionReadProject,
	"GET /v1/projects/{slug}/environments/{environment}/releases":                    authz.OrgActionReadProject,
	"GET /v1/projects/{slug}/environments/{environment}/release-sets":                authz.OrgActionReadProject,
	"GET /v1/projects/{slug}/environments/{environment}/release-sets/active":         authz.OrgActionReadProject,
	"GET /v1/projects/{slug}/environments/{environment}/release-sets/{release}":      authz.OrgActionReadProject,
	"GET /v1/projects/{slug}/environments/{environment}/state":                       authz.OrgActionReadProject,
	"GET /v1/projects/{slug}/environments/{environment}/diff":                        authz.OrgActionReadProject,
	"GET /v1/projects/{slug}/environments/{environment}/config":                      authz.OrgActionReadProject,
	"GET /v1/projects/{slug}/environments/{environment}/config/diff":                 authz.OrgActionReadProject,
	"GET /v1/projects/{slug}/environments/{environment}/promotion-preview":           authz.OrgActionReadProject,
	"GET /v1/projects/{slug}/environments/{environment}/approvals/{approval}":        authz.OrgActionReadProject,
	"GET /v1/projects/{slug}/environments/{environment}/promotions/{promotion}":      authz.OrgActionReadProject,
	"GET /v1/projects/{slug}/environments/{environment}/promotions":                  authz.OrgActionReadProject,
	"GET /v1/projects/{slug}/delete-preview":                                         authz.OrgActionReadProject,
	"POST /v1/projects/{slug}/deployment-readiness":                                  authz.OrgActionReadProject,
	"POST /v1/projects/{slug}/environments/{environment}/promotion-preview":          authz.OrgActionReadProject,
	"GET /v1/projects/{slug}/submissions/{attempt_id}":                               authz.OrgActionReadProject,
	"POST /v1/projects/{slug}/environments":                                          authz.OrgActionConfigureProject,
	"PUT /v1/projects/{slug}/environments/{environment}/config":                      authz.OrgActionConfigureProject,
	"PUT /v1/projects/{slug}/environments/{environment}/workloads/{workload}/routes": authz.OrgActionConfigureProject,
	"DELETE /v1/projects/{slug}/exclusions/{slug2}":                                  authz.OrgActionConfigureProject,
	// Release secrets need the same roles as app secrets; values are sealed on write.
	"POST /v1/projects/{slug}/release-secrets": authz.OrgActionConfigureProject,
	"POST /v1/projects":                        authz.OrgActionDeployProject,
	"POST /v1/projects/scan":                   authz.OrgActionDeployProject,
	"POST /v1/projects/{slug}/environments/{environment}/qualifications":                           authz.OrgActionDeployProject,
	"POST /v1/projects/{slug}/environments/{environment}/release-sets":                             authz.OrgActionDeployProject,
	"POST /v1/projects/{slug}/environments/{environment}/promote":                                  authz.OrgActionDeployProject,
	"POST /v1/projects/{slug}/environments/{environment}/promotions/{promotion}/rollback":          authz.OrgActionDeployProject,
	"POST /v1/projects/{slug}/safe-deploy":                                                         authz.OrgActionDeployProject,
	"POST /v1/projects/{slug}/environments/{environment}/promotions/{promotion}/recover-migration": authz.OrgActionDeployProject,
	"POST /v1/projects/{slug}/environments/{environment}/promotions/{promotion}/{action}":          authz.OrgActionDeployProject,
	"POST /v1/projects/{slug}/submissions/{attempt_id}/{action}":                                   authz.OrgActionDeployProject,
	"PATCH /v1/projects/{slug}":                                                                    authz.OrgActionManageProjectSecurity,
	"PATCH /v1/projects/{slug}/environments/{environment}":                                         authz.OrgActionManageProjectSecurity,
	"PUT /v1/projects/{slug}/environments/{environment}/workloads/{workload}/policies":             authz.OrgActionManageProjectSecurity,
	"POST /v1/projects/{slug}/environments/{environment}/approvals":                                authz.OrgActionApproveProjectEnvironment,
	"DELETE /v1/projects/{slug}":                                                                   authz.OrgActionDeleteProject,
	"DELETE /v1/projects/{slug}/environments/{environment}":                                        authz.OrgActionDeleteProject,
}

// preCreationRoutes may name a project that its first deploy will create.
// They create nothing and are reviewed like collection requests.
var preCreationRoutes = map[string]bool{
	"POST /v1/projects/{slug}/deployment-readiness": true,
}

func preCreationRoute(pattern string) bool {
	return preCreationRoutes[strings.Replace(pattern, "/v1/orgs/{organization}/projects", "/v1/projects", 1)]
}

func Action(pattern string) (authz.OrgAction, bool) {
	switch pattern {
	case "POST /dashboard/projects/{slug}/update":
		return authz.OrgActionManageProjectSecurity, true
	case "POST /dashboard/projects/{slug}/delete":
		return authz.OrgActionDeleteProject, true
	}
	pattern = strings.Replace(pattern, "/v1/orgs/{organization}/projects", "/v1/projects", 1)
	if pattern == "POST /v1/projects/{slug}/deploy" || pattern == "POST /v1/projects/{slug}/scan" {
		return authz.OrgActionDeployProject, true
	}
	action, ok := routeActions[pattern]
	return action, ok
}
