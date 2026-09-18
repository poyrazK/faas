package tcpd

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// InstanceSource is the authoritative instance projection needed by the raw
// TCP edge. Keeping it narrow lets tcpd use either PgStore or MemStore.
type InstanceSource interface {
	ListInstancesForApp(ctx context.Context, appID string) ([]state.Instance, error)
}

// Admitter is the scheduler slice used when an app has no running instance.
// The scheduler remains the owner of lifecycle state; tcpd only consumes the
// identity returned by the admission call.
type Admitter interface {
	AdmitInstance(ctx context.Context, appID, deploymentID, scope, trigger string) (instanceID, nodeID, deploymentIDOut, wakeID string, method int32, atCapacity bool, port int, err error)
}

// StoreTargetResolver selects a running instance for a TCP route and asks the
// scheduler to admit one when the app is parked. It intentionally does not
// cache targets: instance lifecycle changes are authoritative in schedd/DB,
// and raw sessions are long-lived enough that stale process-local targets are
// particularly surprising.
type StoreTargetResolver struct {
	Instances InstanceSource
	Admitter  Admitter

	mu      sync.Mutex
	cursors map[string]uint64
}

// ResolveTarget implements TargetResolver.
func (r *StoreTargetResolver) ResolveTarget(ctx context.Context, route Route) (gateway.Target, error) {
	if r == nil || r.Instances == nil {
		return gateway.Target{}, errors.New("tcpd target resolver has no instance source")
	}
	if err := ValidateRoute(route); err != nil {
		return gateway.Target{}, err
	}
	instances, err := r.Instances.ListInstancesForApp(ctx, route.AppID)
	if err != nil {
		return gateway.Target{}, fmt.Errorf("list instances for app %q: %w", route.AppID, err)
	}
	var running []state.Instance
	for _, instance := range instances {
		if instance.State == string(state.StateRunning) && instance.ID != "" && instance.NodeID != "" {
			running = append(running, instance)
		}
	}
	if len(running) > 0 {
		r.mu.Lock()
		if r.cursors == nil {
			r.cursors = make(map[string]uint64)
		}
		idx := r.cursors[route.AppID] % uint64(len(running))
		r.cursors[route.AppID]++
		r.mu.Unlock()
		instance := running[idx]
		return gateway.Target{
			AppID:        route.AppID,
			NodeID:       instance.NodeID,
			InstanceID:   instance.ID,
			WakeID:       instance.WakeID,
			DeploymentID: instance.DeploymentID,
			Port:         route.GuestPort,
		}, nil
	}

	if r.Admitter == nil {
		return gateway.Target{}, fmt.Errorf("app %q has no running instance and TCP admission is unavailable", route.AppID)
	}
	instanceID, nodeID, deploymentID, wakeID, _, atCapacity, _, err := r.Admitter.AdmitInstance(ctx, route.AppID, "", "", "gateway")
	if err != nil {
		return gateway.Target{}, fmt.Errorf("admit app %q for TCP listener: %w", route.AppID, err)
	}
	if atCapacity || instanceID == "" || nodeID == "" {
		return gateway.Target{}, fmt.Errorf("app %q has no routable instance after TCP admission", route.AppID)
	}
	return gateway.Target{
		AppID:        route.AppID,
		NodeID:       nodeID,
		InstanceID:   instanceID,
		WakeID:       wakeID,
		DeploymentID: deploymentID,
		Port:         route.GuestPort,
	}, nil
}
