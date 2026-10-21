// adr: 20261021090000
package sched

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// rehearsalAckVMM acknowledges the requested containment like a current vmmd.
type rehearsalAckVMM struct{ *fakeVMM }

func (v rehearsalAckVMM) CreateColdBoot(ctx context.Context, node, instance string, app AppSpec) (*WakeOutcome, error) {
	out, err := v.fakeVMM.CreateColdBoot(ctx, node, instance, app)
	if out != nil {
		out.RehearsalRunID = app.RehearsalRunID
	}
	return out, err
}

func TestContainedBootPointsPlatformAPIAtRunLocalEndpoint(t *testing.T) {
	for _, run := range []string{"", strings.Repeat("a", 32)} {
		t.Run(map[bool]string{false: "ordinary", true: "contained"}[run != ""], func(t *testing.T) {
			vmm := rehearsalAckVMM{&fakeVMM{}}
			e := &Engine{vmm: vmm}
			customer := make([]fcvm.APIEnvEntry, 1, 2)
			customer[0] = fcvm.APIEnvEntry{Key: api.ScenarioPlatformAPIURLEnv, Value: "https://api.gregale.dev"}
			spec := AppSpec{AppID: "app", APIEnv: customer}
			ctx := withBootPrepared(t.Context(), bootPrepared{instanceID: "instance", rehearsalRunID: run, haveRehearsal: true,
				capture: state.InstanceApplicationStandardAdmission{}, haveCapture: true})
			if _, err := e.createRuntimeWithStandards(ctx, "node", "instance", string(state.StateColdBooting), spec, nil, false); err != nil {
				t.Fatal(err)
			}
			got := vmm.lastColdBootSpec.APIEnv
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
