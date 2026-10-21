package projectaccess

// adr: 20261021081828

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/authz"
)

func TestReleaseOperationActions(t *testing.T) {
	for pattern, want := range map[string]authz.OrgAction{
		"POST /v1/orgs/{organization}/projects/{slug}/safe-deploy":                                                         authz.OrgActionDeployProject,
		"GET /v1/orgs/{organization}/projects/{slug}/submissions/{attempt_id}":                                             authz.OrgActionReadProject,
		"POST /v1/orgs/{organization}/projects/{slug}/submissions/{attempt_id}/{action}":                                   authz.OrgActionDeployProject,
		"POST /v1/orgs/{organization}/projects/{slug}/release-secrets":                                                     authz.OrgActionConfigureProject,
		"POST /v1/orgs/{organization}/projects/{slug}/deployment-readiness":                                                authz.OrgActionReadProject,
		"POST /v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotion-preview":                        authz.OrgActionReadProject,
		"POST /v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotions/{promotion}/{action}":          authz.OrgActionDeployProject,
		"POST /v1/orgs/{organization}/projects/{slug}/environments/{environment}/promotions/{promotion}/recover-migration": authz.OrgActionDeployProject,
		// Personal URLs for company projects in the actor's namespace use the same table.
		"POST /v1/projects/{slug}/release-secrets":      authz.OrgActionConfigureProject,
		"POST /v1/projects/{slug}/deployment-readiness": authz.OrgActionReadProject,
	} {
		got, ok := Action(pattern)
		if !ok || got != want {
			t.Errorf("%s = %q %v; want %q", pattern, got, ok, want)
		}
	}
	// The slug-less collection form would resolve a body slug in a shared namespace.
	if _, ok := Action("POST /v1/orgs/{organization}/projects/safe-deploy"); ok {
		t.Error("collection safe deploy must stay unreviewed")
	}
	for pattern, want := range map[string]bool{
		"POST /v1/projects/{slug}/deployment-readiness":                        true,
		"POST /v1/orgs/{organization}/projects/{slug}/deployment-readiness":    true,
		"POST /v1/orgs/{organization}/projects/{slug}/release-secrets":         false,
		"GET /v1/orgs/{organization}/projects/{slug}/submissions/{attempt_id}": false,
	} {
		if got := preCreationRoute(pattern); got != want {
			t.Errorf("preCreationRoute(%s) = %v; want %v", pattern, got, want)
		}
	}
}
