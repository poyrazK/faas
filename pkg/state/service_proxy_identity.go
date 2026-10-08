package state

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// LiveInstancesByHostIP provides a fresh, indexed source-identity read for
// guest DNS and HTTP. VM slots can be recycled immediately, so callers must
// not authenticate a graph hop from a time-based HostIP cache. At most two
// distinct app owners are returned; deployment overlap cannot hide an owner.
func (s *PgStore) LiveInstancesByHostIP(ctx context.Context, nodeID, hostIP string) ([]Instance, error) {
	address, err := netip.ParseAddr(hostIP)
	if err != nil {
		return nil, ErrInvalidArgument
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
