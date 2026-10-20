// spec: §6 — schedd owns admission; a deployment cutover mid-wake fails closed.
package sched_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Admission reuses a coordinated wake's app/account/owner reads but must still
// re-resolve the live deployment: a cutover between selection and admission
// fails closed with the retryable conflict and never reaches vmmd.
func TestAdmissionReusingWakeSelectionFailsClosedOnCutover(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cutover bool
	}{{"same_deployment", false}, {"cutover", true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			_, app, dep := sched.SeedApp(t, store, api.PlanPro, 256, 5)
			vmm := &sched.FakeVMM{}
			e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
			selected, err := e.ResolveWakeEnvironment(ctx, app.ID, nil)
			if err != nil || (*selected.Deployment()).ID != dep.ID || !selected.ReusableFor(app.ID) {
				t.Fatalf("selection: %+v %v", (*selected.Deployment()), err)
			}
			if tc.cutover {
				if _, err := store.CreateDeployment(ctx, state.Deployment{
					AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:def", Status: state.DeployLive,
				}); err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentSuperseded(ctx, dep.ID); err != nil {
					t.Fatal(err)
				}
			}
			result, err := e.AdmitAndDispatchWithOptions(sched.WithWakeEnvironment(ctx, selected), app.ID, "", "normal", "", false, false)
			if !tc.cutover {
				if err != nil || result.InstanceID == "" || (*vmm.ColdBoots())+(*vmm.Restores()) != 1 {
					t.Fatalf("reused selection did not admit: %+v %v boots=%d", result, err, (*vmm.ColdBoots())+(*vmm.Restores()))
				}
				return
			}
			var problem *api.Problem
			if !errors.As(err, &problem) || problem.Status != http.StatusConflict || problem.Code != api.CodeConflict {
				t.Fatalf("cutover after selection was admitted: %+v %v", result, err)
			}
			if result.InstanceID != "" || (*vmm.ColdBoots())+(*vmm.Restores()) != 0 || (*e.XLedger()).ResidentRAM() != 0 {
				t.Fatalf("cutover reached vmmd or held capacity: %+v boots=%d ram=%d", result, (*vmm.ColdBoots())+(*vmm.Restores()), (*e.XLedger()).ResidentRAM())
			}
		})
	}
}
