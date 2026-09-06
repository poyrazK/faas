package scheddgrpc

import (
	"context"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

// EnsureWakeCapacity reports the primary instance and every independently
// ready sibling. The callback is synchronous and never runs after return.
// Old servers ignore desired_instances and still return their primary target.
func (c *Client) EnsureWakeCapacity(ctx context.Context, appID, trigger string, desired int, report func(instanceID, nodeID, deploymentID, wakeID string, method int32, port int)) error {
	desired = max(1, min(desired, api.ScaleUpMaxBurstPerTick))
	response, err := c.cli.EnsureWake(ctx, &scheddpb.EnsureWakeRequest{AppId: appID, Trigger: trigger, DesiredInstances: int32(desired)})
	if err != nil {
		return liftErr(err)
	}
	if report != nil {
		report(response.GetInstanceId(), response.GetNodeId(), response.GetDeploymentId(), response.GetWakeId(), int32(response.GetMethod()), int(response.GetPort()))
		for _, instance := range response.GetAdditionalInstances() {
			report(instance.GetInstanceId(), instance.GetNodeId(), instance.GetDeploymentId(), instance.GetWakeId(), int32(instance.GetMethod()), int(instance.GetPort()))
		}
	}
	return nil
}
