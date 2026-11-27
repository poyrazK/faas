package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// AppFloorResidencyStore lets meterd tell a min-instances floor the platform
// is holding from one it has failed to hold. Optional on Store: a store
// without it keeps billing every floor gap (the pre-2026-11-21 behaviour).
type AppFloorResidencyStore interface {
	// AppResidentSince reports whether any of appID's instances was billably
	// resident at some point in [since, before). An instance on a node that
	// stopped heartbeating counts only up to that node's last heartbeat, the
	// same clamp InstanceBillingSeconds applies.
	AppResidentSince(ctx context.Context, appID string, since, before time.Time) (bool, error)
}

// AppResidentSince implements AppFloorResidencyStore from the instance
// billing ledger.
func (s *PgStore) AppResidentSince(ctx context.Context, appID string, since, before time.Time) (bool, error) {
	if appID == "" || !since.Before(before) {
		return false, nil
	}
	var resident bool
	err := s.pool.QueryRow(ctx, `
		select exists (
		  select 1
		    from instance_billing_intervals b
		    join instances i on i.id = b.instance_id
		    left join compute_nodes n
		      on n.id = i.node_id
		     and n.last_heartbeat_at < clock_timestamp() - make_interval(secs => $4)
		   where i.app_id = $1::uuid
		     and b.started_at < $3
		     and (b.ended_at > $2 or b.ended_at is null)
		     and least(coalesce(b.ended_at, $3::timestamptz), coalesce(n.last_heartbeat_at, 'infinity'::timestamptz)) > $2
		)`, appID, since.UTC(), before.UTC(), float64(api.DeadNodeReconcilerStalenessSeconds)).Scan(&resident)
	if err != nil {
		return false, fmt.Errorf("state: app resident since: %w", err)
	}
	return resident, nil
}
