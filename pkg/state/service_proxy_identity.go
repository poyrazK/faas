package state

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// LiveInstancesByHostIP provides a fresh, indexed source-identity read for
// guest DNS and HTTP. Managed scheduled Jobs receive an app/deployment
// identity only while a claimed scheduled task and its active GitOps release
// prove the exact frozen Job workload. VM slots can be recycled immediately,
// so callers must not authenticate a graph hop from a time-based HostIP cache.
// At most two distinct app owners are returned; deployment overlap cannot hide
// an owner.
func (s *PgStore) LiveInstancesByHostIP(ctx context.Context, nodeID, hostIP string) ([]Instance, error) {
	address, err := netip.ParseAddr(hostIP)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	// Every guest service call and DNS answer reads this. The managed-Job
	// attribution query took 0.6-0.8 s to plan per call through the
	// transaction pooler (Hunt 14 H14-33), so it runs only when a job task
	// without an app is live at the address.
	live, err := sqlc.New().LiveServiceProxyInstanceRowsByHostIP(ctx, s.pool, sqlc.LiveServiceProxyInstanceRowsByHostIPParams{
		HostIp: address.String(), NodeName: nodeID,
	})
	if err != nil {
		return nil, fmt.Errorf("state: lookup live instance by host IP: %w", err)
	}
	needsJobAttribution := false
	for _, row := range live {
		needsJobAttribution = needsJobAttribution || row.NeedsJobAttribution
	}
	if !needsJobAttribution {
		return liveServiceProxyIdentities(live, address.String(), nodeID), nil
	}
	rows, err := sqlc.New().LiveServiceProxyIdentitiesByHostIP(ctx, s.pool, sqlc.LiveServiceProxyIdentitiesByHostIPParams{
		HostIp: address.String(), NodeName: nodeID,
	})
	if err != nil {
		return nil, fmt.Errorf("state: lookup live instance by host IP: %w", err)
	}
	var instances []Instance
	for _, row := range rows {
		instances = append(instances, Instance{AppID: row.AppID, DeploymentID: row.DeploymentID,
			HostIP: address.String(), NodeID: nodeID, State: string(StateRunning)})
	}
	return instances, nil
}

// liveServiceProxyIdentities groups instance rows by app exactly as
// LiveServiceProxyIdentitiesByHostIP does: at most two app owners, and a
// deployment only when every row of the owner names the same one.
func liveServiceProxyIdentities(rows []sqlc.LiveServiceProxyInstanceRowsByHostIPRow, hostIP, nodeID string) []Instance {
	type owner struct {
		deployment string
		ambiguous  bool
	}
	var order []string
	owners := make(map[string]*owner)
	for _, row := range rows {
		o, seen := owners[row.AppID]
		if !seen {
			o = &owner{deployment: row.DeploymentID, ambiguous: row.DeploymentID == ""}
			owners[row.AppID] = o
			order = append(order, row.AppID)
			continue
		}
		if row.DeploymentID == "" || row.DeploymentID != o.deployment {
			o.ambiguous = true
		}
	}
	var instances []Instance
	for _, app := range order {
		if len(instances) == 2 {
			break
		}
		deployment := owners[app].deployment
		if owners[app].ambiguous {
			deployment = ""
		}
		instances = append(instances, Instance{AppID: app, DeploymentID: deployment,
			HostIP: hostIP, NodeID: nodeID, State: string(StateRunning)})
	}
	return instances
}
