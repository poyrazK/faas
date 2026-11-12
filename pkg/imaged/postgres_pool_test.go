// adr: 20261104141122
package imaged

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStampPostgresPool(t *testing.T) {
	released := func(platformflags.Subject) bool { return true }
	for _, tc := range []struct {
		name     string
		released func(platformflags.Subject) bool
		plan     api.Plan
		perVM    int
		env      map[string]string
		want     string
	}{
		{name: "flag unset", plan: api.PlanPro},
		{name: "not released", released: func(platformflags.Subject) bool { return false }, plan: api.PlanPro},
		{name: "free plan concurrency", released: released, plan: api.PlanFree, want: "4"},
		{name: "hobby plan concurrency", released: released, plan: api.PlanHobby, want: "5"},
		{name: "pro capped", released: released, plan: api.PlanPro, want: "10"},
		{name: "declared per-instance concurrency", released: released, plan: api.PlanScale, perVM: 3, want: "3"},
		{name: "image value kept", released: released, plan: api.PlanPro, env: map[string]string{api.PostgresPoolMaxEnv: "7"}, want: "7"},
		{name: "unknown plan", released: released, plan: "legacy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := (&Handler{}).WithPostgresPoolSizingRelease(tc.released)
			app := state.App{ID: "app", ScalingPolicy: &state.ScalingPolicy{PerInstanceConcurrency: tc.perVM}}
			got := h.stampPostgresPool(api.AppManifest{Env: tc.env}, app, state.Account{ID: "acct", Plan: tc.plan})
			if got.Env[api.PostgresPoolMaxEnv] != tc.want {
				t.Fatalf("%s = %q, want %q", api.PostgresPoolMaxEnv, got.Env[api.PostgresPoolMaxEnv], tc.want)
			}
			if tc.env == nil && tc.want == "" && got.Env != nil {
				t.Fatalf("env changed without a stamp: %v", got.Env)
			}
		})
	}
}
