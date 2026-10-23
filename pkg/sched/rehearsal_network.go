// adr: 20261017201005
package sched

import (
	"context"
	"errors"
	"fmt"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func (e *Engine) rehearsalNetworkRun(ctx context.Context, appID string) (string, error) {
	return e.store.ScenarioRehearsalNetworkRun(ctx, appID)
}

// appendRehearsalPlatformAPI points a contained guest's platform API client at
// the run-local endpoint, where queue work is captured (ADR-20261021090000).
// Containment blocks the public API, so a customer value could never be used.
// The entry follows customer entries, so it wins over plaintext env; a secret
// of the same name still wins in the guest and its calls stay blocked.
func appendRehearsalPlatformAPI(env []fcvm.APIEnvEntry) []fcvm.APIEnvEntry {
	return append(env[:len(env):len(env)], fcvm.APIEnvEntry{Key: api.ScenarioPlatformAPIURLEnv,
		Value: fmt.Sprintf("http://%s:%d", api.ScenarioPlatformAPIHost, api.ServiceBindingPort)})
}

func (c *VMMClient) confirmRehearsalNetwork(ctx context.Context, app AppSpec, instance string, resp *vmmdpb.WakeResponse) error {
	if app.RehearsalRunID == "" || (resp.GetRehearsalRunId() == app.RehearsalRunID && resp.GetInstance() == instance && resp.GetNetns() != "") {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
	defer cancel()
	_ = c.Destroy(cleanupCtx, instance)
	return errors.New("runtime did not acknowledge the requested rehearsal network containment")
}

func (e *Engine) createRehearsalCheckedRuntime(ctx context.Context, nodeID, instanceID string, spec AppSpec, snap *SnapshotRef, paused bool) (*WakeOutcome, error) {
	var out *WakeOutcome
	var err error
	switch {
	case snap == nil:
		out, err = e.vmm.CreateColdBoot(ctx, nodeID, instanceID, spec)
	case !paused:
		out, err = e.vmm.CreateFromSnapshot(ctx, nodeID, instanceID, spec, *snap)
	default:
		vmm, ok := e.vmm.(PausedRestoreVMM)
		if !ok {
			return nil, runtimeadmission.ErrUnavailable
		}
		out, err = vmm.CreatePausedFromSnapshot(ctx, nodeID, instanceID, spec, *snap)
	}
	if err != nil {
		return nil, err
	}
	if spec.RehearsalRunID != "" && (out == nil || out.RehearsalRunID != spec.RehearsalRunID || out.Instance != instanceID || out.Netns == "") {
		e.bestEffortDestroy(ctx, nodeID, instanceID)
		return nil, errors.New("runtime did not acknowledge rehearsal network containment")
	}
	return out, nil
}
