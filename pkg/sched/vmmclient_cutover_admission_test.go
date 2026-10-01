// adr: 394 — retain the durable fence error across the owner RPC.
package sched_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestVMMClientCutoverFenceSurvivesBootAndTaskRestore(t *testing.T) {
	for _, kind := range []string{"boot", "app_task"} {
		t.Run(kind, func(t *testing.T) {
			client := newClient(t, &fakeVMM{wakeFn: func(context.Context, fcvm.WakeRequest) (*fcvm.Instance, error) {
				return nil, fcvm.ErrAppAdmissionFenced
			}})
			app := sched.AppSpec{BaseKey: "base.ext4", LayerKey: "app.ext4", VCPUCount: 1, MemSizeMiB: 128, AppID: "app-fenced"}
			var err error
			if kind == "boot" {
				_, err = client.CreateColdBoot(t.Context(), "source-instance", app)
			} else {
				_, err = client.RestoreAppTask(t.Context(), sched.AppTaskRestoreSpec{Instance: "source-task", DeploymentID: "deployment", App: app})
			}
			problem := api.AsProblem(err)
			if !errors.Is(err, state.ErrManagedPostgresAdmissionFenced) || problem == nil || problem.Code != api.CodeDatabaseCutoverFenced || problem.Status != 409 {
				t.Fatalf("owner RPC lost cutover denial: %v", err)
			}
		})
	}
}
