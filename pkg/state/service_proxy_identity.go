package state

import (
	"context"
	"fmt"
	"net/netip"
)

// LiveInstancesByHostIP provides a fresh, indexed source-identity read for
// the guest service proxy. VM slots can be recycled immediately, so callers
// must not authenticate a graph hop from a time-based HostIP cache.
func (s *PgStore) LiveInstancesByHostIP(ctx context.Context, nodeID, hostIP string) ([]Instance, error) {
	address, err := netip.ParseAddr(hostIP)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	query := `select distinct i.app_id::text, i.deployment_id::text
		from instances i
		where i.host_ip = $1::inet and i.state in ('running', 'draining')`
	args := []any{address.String()}
	if nodeID != "" {
		query += ` and exists (select 1 from compute_nodes n where n.id = i.node_id and n.name = $2)`
		args = append(args, nodeID)
	}
	// Two distinct identities suffice to detect ambiguity; duplicates from
	// the same deployment are collapsed by DISTINCT.
	query += ` limit 2`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("state: lookup live instance by host IP: %w", err)
	}
	defer rows.Close()
	var instances []Instance
	for rows.Next() {
		var instance Instance
		if err := rows.Scan(&instance.AppID, &instance.DeploymentID); err != nil {
			return nil, fmt.Errorf("state: scan live instance by host IP: %w", err)
		}
		instance.HostIP = address.String()
		instance.NodeID = nodeID
		instance.State = string(StateRunning)
		instances = append(instances, instance)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate live instances by host IP: %w", err)
	}
	return instances, nil
}
