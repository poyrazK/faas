package sched

import (
	"context"
	"errors"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/state"
)

// AdmitTrafficInstance is AdmitInstance for request traffic: the gateway's
// deployment-routed wake and the raw TCP and UDP edges. A request for an
// explicitly parked (evicted_cold) app un-parks it the way EnsureWake does.
// AdmitInstance admitted the app without claiming the parked -> active
// lifecycle, so the reaper's parked-app sweep destroyed the waking VM (or
// re-parked it right after it served), and every request after `gregale
// park` answered 503 (container qualification, 2026-10-27).
//
// A deployment smoke admits a candidate before cutover and never changes
// the app's lifecycle, so it keeps the plain admission.
func (e *Engine) AdmitTrafficInstance(ctx context.Context, appID, deploymentID, scope, trigger string) (WakeResult, error) {
	if trigger != TriggerDeploymentSmoke {
		if app, err := e.store.AppByID(ctx, appID); err == nil && app.Status == state.AppEvictedCold {
			return e.wakeParkedApp(ctx, appID, deploymentID, scope, trigger)
		}
	}
	return e.AdmitInstance(ctx, appID, deploymentID, scope, trigger)
}

func (e *Engine) wakeParkedApp(ctx context.Context, appID, deploymentID, scope, trigger string) (WakeResult, error) {
	out, err := e.EnsureWakeForDeployment(ctx, appID, deploymentID, scope, trigger)
	if err == nil {
		err = out.Err
	}
	// AdmitInstance reports a full app as a benign AtCapacity result. A
	// worker or job app has no request listener; keep that error.
	if errors.Is(err, ErrAtCapacity) && !errors.Is(err, ErrNoRequestListener) {
		return WakeResult{AtCapacity: true}, nil
	}
	if err != nil {
		return WakeResult{}, err
	}
	if out.Instance == nil {
		return WakeResult{AtCapacity: true}, nil
	}
	method := vmmdpb.WakeMethod_WAKE_RESTORE
	if out.Instance.ColdBoot {
		method = vmmdpb.WakeMethod_WAKE_COLD_BOOT
	}
	return WakeResult{
		InstanceID: out.Instance.InstanceID, NodeID: out.Instance.NodeID, Method: method,
		WakeID: out.Instance.WakeID, Port: int(out.Instance.Port), DeploymentID: out.Instance.DeploymentID,
		Identity: out.Instance.Identity,
	}, nil
}
