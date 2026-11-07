package state

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261029180000
func TestPlanChangeOverageCap(t *testing.T) {
	hobby, _ := api.PlanHobby.DefaultOverageCapCents()
	pro, _ := api.PlanPro.DefaultOverageCapCents()
	cents := func(v int64) *int64 { return &v }
	for _, tc := range []struct {
		name     string
		from, to api.Plan
		current  *int64
		want     *int64
	}{
		{"free to paid without a cap", api.PlanFree, api.PlanHobby, nil, cents(hobby)},
		{"free to paid keeps a cap set on free", api.PlanFree, api.PlanHobby, cents(50), cents(50)},
		{"default follows an upgrade", api.PlanHobby, api.PlanPro, cents(hobby), cents(pro)},
		{"default follows a downgrade", api.PlanPro, api.PlanHobby, cents(pro), cents(hobby)},
		{"default removed on free", api.PlanPro, api.PlanFree, cents(pro), nil},
		{"same plan keeps the cap", api.PlanPro, api.PlanPro, cents(pro), cents(pro)},
		{"customer cap kept", api.PlanHobby, api.PlanPro, cents(777), cents(777)},
		{"pause switch kept", api.PlanHobby, api.PlanPro, cents(0), cents(0)},
		{"cleared cap kept on a paid change", api.PlanHobby, api.PlanScale, nil, nil},
		{"free stays uncapped", api.PlanFree, api.PlanFree, nil, nil},
		// ADR-20261105120000: a cap above the target's self-service ceiling
		// comes down to it.
		{"scale cap capped on a downgrade", api.PlanScale, api.PlanHobby, cents(300_000), cents(20_000)},
		{"free cap capped on an upgrade", api.PlanFree, api.PlanHobby, cents(1_000_000), cents(20_000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := planChangeOverageCap(tc.from, tc.to, tc.current)
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("planChangeOverageCap(%s, %s) = %v, want %v", tc.from, tc.to, capOrNone(got), capOrNone(tc.want))
			}
		})
	}
}

func capOrNone(v *int64) any {
	if v == nil {
		return "none"
	}
	return *v
}
