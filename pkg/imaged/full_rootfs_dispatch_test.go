package imaged

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// spec: §4.6
// adr: 147 — amendment 2026-11-28: a Free deployment of an image that is not
// built on a Gregale runtime base takes the self-contained path by default.
// Production, 2026-11-28: a Free signup's image deploy failed three times with
// "plan free does not auto-dispatch to full-rootfs", while the same Dockerfile
// deployed from GitHub went live.
func TestDispatchFullRootfsPlanGate(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name         string
		plan         api.Plan
		allowAuto    *bool // nil uses the plan default
		override     *bool
		wantFullRoot bool
	}{
		{name: "free default", plan: api.PlanFree, wantFullRoot: true},
		{name: "hobby default", plan: api.PlanHobby, wantFullRoot: true},
		{name: "free auto off", plan: api.PlanFree, allowAuto: &no},
		{name: "free auto off, override on", plan: api.PlanFree, allowAuto: &no, override: &yes, wantFullRoot: true},
		{name: "free override off", plan: api.PlanFree, override: &no},
		{name: "unknown plan", plan: api.Plan("unknown"), allowAuto: &yes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// fakePuller is not an oci.ManifestPuller, so the full-rootfs
			// build stops at its first step with a recognizable error.
			h := New(state.NewMemStore(), &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger())
			dep := state.Deployment{ID: "dep-1", FullRootfsAllowAuto: api.FullRootfsAllowAutoDefault[tt.plan], FullRootfsOverride: tt.override}
			if tt.allowAuto != nil {
				dep.FullRootfsAllowAuto = *tt.allowAuto
			}
			err := h.dispatchFullRootfs(t.Context(), state.App{Slug: "ws-node"}, dep, state.Account{Plan: tt.plan},
				api.AppManifest{}, nil, preparedContainerWorkload{})
			if gotFullRoot := !errors.Is(err, oci.ErrLayersNotAboveBase); gotFullRoot != tt.wantFullRoot {
				t.Fatalf("took full-rootfs = %v (err %v), want %v", gotFullRoot, err, tt.wantFullRoot)
			}
			if tt.wantFullRoot && (err == nil || !strings.Contains(err.Error(), "full-rootfs requires oci.ManifestPuller")) {
				t.Fatalf("err = %v, want the full-rootfs build to start", err)
			}
			if !tt.wantFullRoot && tt.override == nil && strings.Contains(err.Error(), "FullRootfsOverride=&true") {
				t.Fatalf("err = %v, want customer-facing text without Go syntax", err)
			}
		})
	}
}
