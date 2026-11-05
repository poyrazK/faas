package state

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261026170512 in a frozen release graph: a Web_UI caller that declared
// billing_api reaches it as billing-api, with the target's allowlist, call
// scope and the caller's reliability policy matched by service name.
func TestProjectServiceGraphUsesServiceDNSNames(t *testing.T) {
	allowed := []string{"web-ui"}
	scopes := api.ServiceCallerScopes{"web-ui": {Methods: []string{"GET"}, PathPrefixes: []string{"/invoices"}}}
	caller := projectServiceMember{AppID: "web-id", AccountID: "account", ProjectID: "project", Name: "Web_UI", Slug: "project-web", DeploymentID: "web-dep", Scope: "production", Live: true,
		Settings: ProjectEnvironmentWorkloadSettings{Manifest: AppManifest{ServiceBindings: api.ServiceBindingsForTargets([]string{"billing_api"}),
			ServiceReliability: map[string]api.ServiceReliabilityPolicy{"billing_api": {TimeoutMS: 700}}}}}
	target := projectServiceMember{AppID: "billing-id", AccountID: "account", ProjectID: "project", Name: "billing_api", Slug: "project-billing", DeploymentID: "billing-dep", Scope: "production", Live: true,
		Settings: ProjectEnvironmentWorkloadSettings{Manifest: AppManifest{AllowedServiceCallers: &allowed, AllowedServiceCallScopes: &scopes}}}
	var err error
	if caller.Hash, err = WorkloadSettingsHash(caller.Settings); err != nil {
		t.Fatal(err)
	}
	if target.Hash, err = WorkloadSettingsHash(target.Settings); err != nil {
		t.Fatal(err)
	}
	input := projectServiceGraphInput{Caller: caller, Graphs: []projectServiceGraph{{ID: "graph", Members: []projectServiceMember{caller, target}}}}
	route, managed, found, err := resolveProjectServiceGraph(input, normalizeProjectServiceName("billing-api"))
	if err != nil || !managed || !found || route.TargetAppID != target.AppID {
		t.Fatalf("billing-api in the release graph = %+v managed=%t found=%t err=%v; want billing_api", route, managed, found, err)
	}
	if route.CallScope == nil || !route.CallScope.Allows("GET", "/invoices/7") {
		t.Fatalf("call scope for web-ui lost: %+v", route.CallScope)
	}
	if route.Reliability == nil || route.Reliability.TimeoutMS != 700 {
		t.Fatalf("reliability policy for billing_api lost: %+v", route.Reliability)
	}
}
