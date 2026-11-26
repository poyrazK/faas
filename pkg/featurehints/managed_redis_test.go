package featurehints_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/featurehints"
)

// Prod hunt r52 (rc.381): a Compose `redis:7` service the deploy provides as
// a managed Redis cache was described as "the managed PostgreSQL database
// compose-cache".
func TestProvidedRedisIsDescribedAsACache(t *testing.T) {
	in := featurehints.Input{
		ProjectSlug: "compose",
		Workloads:   []api.PlanWorkload{{Name: "api", AppSlug: "compose-api", Class: "http"}},
		Managed: []api.PlanManaged{{Name: "cache", Kind: "redis", Provision: &api.PlanManagedProvision{Database: "compose-cache",
			Bindings: []api.PlanManagedBinding{{Workload: "api", EnvKey: "REDIS_URL"}}}}},
	}
	var applied string
	for _, h := range featurehints.For(in) {
		if h.ID == featurehints.IDManagedPostgres {
			applied = h.Applied
		}
	}
	if !strings.Contains(applied, "the managed Redis cache compose-cache") || strings.Contains(applied, "PostgreSQL") {
		t.Fatalf("provided redis hint = %q", applied)
	}
}
