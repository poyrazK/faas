package rootfs

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// withAppLayerCapMB shrinks one plan's app-layer cap for the duration of a
// sequential test and returns the limits the build under test will see.
//
// The cap-violation tests used to prove the check by really writing 256–300
// MiB through gzip and onto disk, which made pkg/rootfs one of the slowest
// packages in CI (53 s of its 73 s). The check itself is size-agnostic: a
// 2 MiB input against a 1 MiB cap takes the identical code path and produces
// the identical Problem shape.
//
// Only sequential tests may call this — Go starts t.Parallel tests after
// every sequential test has finished, so the override never overlaps them.
func withAppLayerCapMB(t *testing.T, plan api.Plan, mb int) api.Limits {
	t.Helper()
	base, ok := api.LimitsFor(plan)
	if !ok {
		t.Fatalf("api.LimitsFor(%q) not ok", plan)
	}
	shrunk := base
	shrunk.AppLayerMaxMB = mb
	prev := limitsFor
	limitsFor = func(p api.Plan) (api.Limits, bool) {
		if p == plan {
			return shrunk, true
		}
		return api.LimitsFor(p)
	}
	t.Cleanup(func() { limitsFor = prev })
	return shrunk
}
