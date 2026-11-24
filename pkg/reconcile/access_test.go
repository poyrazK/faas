// adr: 20261124090000
package reconcile_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reconcile"
)

func TestServiceAccessAuth(t *testing.T) {
	for _, tc := range []struct {
		name, access string
		plan         api.Plan
		require      bool
		mode         string
		wantRequire  bool
		wantMode     string
	}{
		{name: "public opens a bearer gate", access: "public", plan: api.PlanPro, require: true, mode: "bearer", wantMode: "open"},
		{name: "public keeps basic auth", access: "public", plan: api.PlanPro, require: true, mode: "basic", wantMode: "basic"},
		{name: "key turns the gate on", access: "key", plan: api.PlanHobby, mode: "open", wantRequire: true, wantMode: "open"},
		{name: "key on Free has no gate", access: "key", plan: api.PlanFree, mode: "open", wantMode: "open"},
		{name: "nothing declared", access: "", plan: api.PlanPro, require: true, mode: "bearer", wantRequire: true, wantMode: "bearer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require, mode := reconcile.ServiceAccessAuth(tc.access, tc.plan, tc.require, tc.mode)
			if require != tc.wantRequire || mode != tc.wantMode {
				t.Fatalf("= %v %q, want %v %q", require, mode, tc.wantRequire, tc.wantMode)
			}
		})
	}
	if require, mode := reconcile.ServiceAccessRemovedAuth(api.PlanPro, "open"); !require || mode != "bearer" {
		t.Fatalf("removed on Pro = %v %q, want the plan defaults", require, mode)
	}
	if _, mode := reconcile.ServiceAccessRemovedAuth(api.PlanPro, "basic"); mode != "basic" {
		t.Fatalf("removed keeps basic auth, got %q", mode)
	}
}
