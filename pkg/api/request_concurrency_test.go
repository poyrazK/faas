// adr: 20261101215228

package api_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRequestConcurrencyResolution(t *testing.T) {
	free, scale := api.MustLimitsFor(api.PlanFree), api.MustLimitsFor(api.PlanScale)
	for _, tc := range []struct {
		name      string
		rc        api.RequestConcurrency
		limits    api.Limits
		requested int
		want      int
		bound     int
	}{
		{"unset, no declaration", api.RequestConcurrency{}, free, 0, 4, 4},
		{"unset, declaration", api.RequestConcurrency{}, scale, 10, 10, 80},
		{"unset, above the bound", api.RequestConcurrency{}, free, 9, 4, 4},
		{"default and max", api.RequestConcurrency{Default: 80, Max: 1000}, free, 0, 80, 1000},
		{"declaration up to max", api.RequestConcurrency{Default: 80, Max: 1000}, free, 500, 500, 1000},
		{"declaration above max", api.RequestConcurrency{Default: 80, Max: 1000}, scale, 5000, 1000, 1000},
		{"default above max", api.RequestConcurrency{Default: 200, Max: 100}, free, 0, 100, 100},
		{"default only stays under the plan bound", api.RequestConcurrency{Default: 80}, free, 0, 4, 4},
		{"max only", api.RequestConcurrency{Max: 1000}, free, 0, 1000, 1000},
	} {
		if got := tc.rc.PerInstance(tc.limits, tc.requested); got != tc.want {
			t.Errorf("%s: PerInstance = %d, want %d", tc.name, got, tc.want)
		}
		if got := tc.rc.Bound(tc.limits); got != tc.bound {
			t.Errorf("%s: Bound = %d, want %d", tc.name, got, tc.bound)
		}
	}
}

// The process value drives Limits.PerInstanceConcurrency, the resolver every
// gateway, schedd and apid caller uses; unset is the plan behaviour.
func TestSetRequestConcurrencyDrivesTheResolver(t *testing.T) {
	t.Cleanup(func() { api.SetRequestConcurrency(api.RequestConcurrency{}) })
	pro := api.MustLimitsFor(api.PlanPro)
	if got := pro.PerInstanceConcurrency(0); got != 25 {
		t.Fatalf("unset: Pro resolves %d, want 25", got)
	}
	api.SetRequestConcurrency(api.RequestConcurrency{Default: 80, Max: api.AppRequestConcurrencyBound + 5})
	if got := pro.PerInstanceConcurrency(0); got != 80 {
		t.Errorf("set: Pro resolves %d, want 80", got)
	}
	if got := api.PlanPro.RequestConcurrencyBound(); got != api.AppRequestConcurrencyBound {
		t.Errorf("max is clamped to %d, got %d", api.AppRequestConcurrencyBound, got)
	}
	if p := api.ValidatePerInstanceConcurrency(api.PlanFree, 500); p != nil {
		t.Errorf("a Free declaration under the max was refused: %v", p.Detail)
	}
	api.SetRequestConcurrency(api.RequestConcurrency{})
	if got := pro.PerInstanceConcurrency(0); got != 25 {
		t.Errorf("unset again: Pro resolves %d, want 25", got)
	}
}
