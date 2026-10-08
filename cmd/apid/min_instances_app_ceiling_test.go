// adr: 071 — the min_instances floor stays reachable.

package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// production-us hunt #5 (H5-38): `gregale app h5-probe --min 3` on an app with
// max_concurrency=1 was accepted (the bound was the plan's 20) and the CLI
// promised three warm instances the scheduler could never keep.
func TestValidateUpdateAppKeepsMinInstancesUnderTheAppCeiling(t *testing.T) {
	acct := state.Account{Plan: api.PlanScale}
	limits := api.MustLimitsFor(acct.Plan)
	n := func(v int) *int { return &v }
	for _, tc := range []struct {
		name string
		app  state.App
		req  api.UpdateAppRequest
		ok   bool
	}{
		{"floor above the app ceiling", state.App{MaxConcurrency: 1}, api.UpdateAppRequest{MinInstances: n(3)}, false},
		{"floor raised with the ceiling", state.App{MaxConcurrency: 1}, api.UpdateAppRequest{MinInstances: n(3), MaxConcurrency: n(4)}, true},
		{"ceiling lowered under the floor", state.App{MaxConcurrency: 4, MinInstances: 3}, api.UpdateAppRequest{MaxConcurrency: n(2)}, false},
		{"ceiling lowered to the floor", state.App{MaxConcurrency: 4, MinInstances: 2}, api.UpdateAppRequest{MaxConcurrency: n(2)}, true},
		{"unset app ceiling uses the plan", state.App{}, api.UpdateAppRequest{MinInstances: n(3)}, true},
	} {
		prob := validateUpdateApp(&tc.req, acct, limits, tc.app)
		if tc.ok && prob != nil {
			t.Fatalf("%s: rejected: %+v", tc.name, prob)
		}
		if !tc.ok && (prob == nil || prob.Code != api.CodeInvalidMinInstances) {
			t.Fatalf("%s: got %+v, want %s", tc.name, prob, api.CodeInvalidMinInstances)
		}
	}
}
