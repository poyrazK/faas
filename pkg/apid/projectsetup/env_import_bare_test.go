package projectsetup

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

// microservices-demo: gRPC targets are bare host:port. A mesh sibling's
// address imports as its mesh address without the opt-in; a raw TCP or
// datastore host:port stays a skipped service address.
func TestPlanEnvImportRewritesBareMeshAddress(t *testing.T) {
	value := func(s string) *string { return &s }
	env := reposcan.ComposeEnv{Services: map[string]map[string]*string{
		"frontend": {"PRODUCT_CATALOG_SERVICE_ADDR": value("productcatalogservice:3550"), "REDIS_ADDR": value("redis:6379")}, "redis": {}, "productcatalogservice": {},
	}, Mesh: map[string]bool{"productcatalogservice": true}}
	got := PlanEnvImport(env, "frontend", []string{"frontend", "productcatalogservice", "redis"}, api.MustLimitsFor(api.PlanPro), true)
	if got.Values["PRODUCT_CATALOG_SERVICE_ADDR"] != "productcatalogservice.svc.gregale:10081" {
		t.Fatalf("values %v skipped %v", got.Values, got.Skipped)
	}
	if got.Skipped["REDIS_ADDR"] != EnvSkipServiceAddress {
		t.Fatalf("raw TCP address: skipped %v", got.Skipped)
	}
	if !ValidSiblingAddress("productcatalogservice.svc.gregale:10081") || ValidSiblingAddress("redis:6379") {
		t.Fatal("ValidSiblingAddress bare form wrong")
	}
	if h, ok := reposcan.BareServiceAddressHost("10.0.0.1:80"); ok {
		t.Fatalf("IP accepted as service %q", h)
	}
}
