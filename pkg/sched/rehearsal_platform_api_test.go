// adr: 20261021090000
package sched_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
)

// rehearsalAckVMM acknowledges the requested containment like a current vmmd.
type rehearsalAckVMM struct{ *sched.FakeVMM }

func (v rehearsalAckVMM) CreateColdBoot(ctx context.Context, node, instance string, app sched.AppSpec) (*sched.WakeOutcome, error) {
	out, err := v.FakeVMM.CreateColdBoot(ctx, node, instance, app)
	if out != nil {
		out.RehearsalRunID = app.RehearsalRunID
	}
	return out, err
}

func TestContainedBootPointsPlatformAPIAtRunLocalEndpoint(t *testing.T) {
	for _, run := range []string{"", strings.Repeat("a", 32)} {
		t.Run(map[bool]string{false: "ordinary", true: "contained"}[run != ""], func(t *testing.T) {
			vmm := rehearsalAckVMM{&sched.FakeVMM{}}
			customer := make([]fcvm.APIEnvEntry, 1, 2)
			customer[0] = fcvm.APIEnvEntry{Key: api.ScenarioPlatformAPIURLEnv, Value: "https://api.gregale.dev"}
			if err := sched.CreateRuntimeWithRehearsalRun(t.Context(), vmm, sched.AppSpec{AppID: "app", APIEnv: customer}, run); err != nil {
				t.Fatal(err)
			}
			got := vmm.LastColdBootSpec().APIEnv
			last := got[len(got)-1]
			if run == "" {
				if len(got) != 1 || last.Value != "https://api.gregale.dev" {
					t.Fatalf("ordinary boot env changed: %+v", got)
				}
				return
			}
			if len(got) != 2 || last.Key != api.ScenarioPlatformAPIURLEnv || last.Value != "http://api.svc.gregale:10081" {
				t.Fatalf("contained boot env = %+v", got)
			}
			if customer[:2][1] != (fcvm.APIEnvEntry{}) {
				t.Fatal("stamping wrote into the caller's env backing array")
			}
		})
	}
}
