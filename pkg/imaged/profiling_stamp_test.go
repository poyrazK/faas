// adr: 819, 1155
package imaged

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestStampProfilingFollowsInstallationFlags pins that no profiling code is
// switched on in a guest unless an installation flag allows it: an app's
// profiling.enabled reaches the guest only with continuous profiling, and
// dormant on-demand collectors only with on-demand captures on a plan that
// includes profiling. The app's other profiling settings are kept, since
// pprof_port also serves on-demand captures.
func TestStampProfilingFollowsInstallationFlags(t *testing.T) {
	profiled := func() *api.ProfilingConfig {
		off := false
		return &api.ProfilingConfig{Enabled: true, WindowSeconds: 15, Kinds: []string{api.ProfileKindCPU, api.ProfileKindHeap}, PprofPort: 6060, AutomaticCaptures: &off}
	}
	cases := []struct {
		name                    string
		continuous, onDemand    bool
		plan                    api.Plan
		profiling               *api.ProfilingConfig
		wantEnabled, wantStamp  bool
		wantProfilingConfigNone bool
	}{
		{name: "both flags off", plan: api.PlanPro, profiling: profiled()},
		{name: "continuous on", continuous: true, plan: api.PlanPro, profiling: profiled(), wantEnabled: true},
		{name: "on-demand on", onDemand: true, plan: api.PlanPro, profiling: profiled(), wantStamp: true},
		{name: "both on", continuous: true, onDemand: true, plan: api.PlanPro, profiling: profiled(), wantEnabled: true, wantStamp: true},
		{name: "on-demand on, plan without profiling", onDemand: true, plan: api.PlanFree, profiling: profiled()},
		{name: "no profiling settings", continuous: true, onDemand: true, plan: api.PlanPro, wantStamp: true, wantProfilingConfigNone: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := (&Handler{}).WithContinuousProfiling(tc.continuous).WithProfilingOnDemand(tc.onDemand)
			in := api.AppManifest{Entrypoint: []string{"/app/server"}, Profiling: tc.profiling}
			got := h.stampProfiling(in, state.App{}, state.Account{Plan: tc.plan})
			if got.ProfilingOnDemand != tc.wantStamp {
				t.Fatalf("profiling_on_demand = %v, want %v", got.ProfilingOnDemand, tc.wantStamp)
			}
			if tc.wantProfilingConfigNone {
				if got.Profiling != nil {
					t.Fatalf("profiling = %+v, want none", got.Profiling)
				}
				return
			}
			if got.Profiling == nil || got.Profiling.Enabled != tc.wantEnabled {
				t.Fatalf("profiling = %+v, want enabled=%v", got.Profiling, tc.wantEnabled)
			}
			if got.Profiling.PprofPort != 6060 || got.Profiling.WindowSeconds != 15 || len(got.Profiling.Kinds) != 2 {
				t.Fatalf("profiling settings changed: %+v", got.Profiling)
			}
			if got.Profiling.AutomaticCaptures != nil {
				t.Fatal("automatic_captures, a control-plane setting, reached the guest manifest")
			}
			if !tc.profiling.Enabled || tc.profiling.AutomaticCaptures == nil {
				t.Fatal("stampProfiling changed the app's own configuration")
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("stamped manifest invalid: %v", err)
			}
		})
	}
}

// TestStampProfilingFollowsCapabilityRelease pins ADR-20261023115741 for
// on-demand captures: collectors reach only deployments of accounts the
// profile-captures preview is released to, decided for the app's account,
// plan and organization.
func TestStampProfilingFollowsCapabilityRelease(t *testing.T) {
	var asked []platformflags.Subject
	h := (&Handler{}).WithProfilingOnDemand(true).WithProfileCapturesRelease(func(s platformflags.Subject) bool {
		asked = append(asked, s)
		return s.AccountID == "released"
	})
	app := state.App{OrgID: "org-1"}
	if got := h.stampProfiling(api.AppManifest{}, app, state.Account{ID: "held", Plan: api.PlanPro}); got.ProfilingOnDemand {
		t.Fatal("collectors stamped for an account captures are not released to")
	}
	if got := h.stampProfiling(api.AppManifest{}, app, state.Account{ID: "released", Plan: api.PlanPro}); !got.ProfilingOnDemand {
		t.Fatal("collectors not stamped for an account captures are released to")
	}
	if len(asked) != 2 || asked[1] != (platformflags.Subject{AccountID: "released", Plan: string(api.PlanPro), OrgID: "org-1"}) {
		t.Fatalf("subjects = %+v", asked)
	}
}
