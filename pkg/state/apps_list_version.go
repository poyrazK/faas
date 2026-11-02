package state

import "context"

// AppsListVersion identifies the rows ListAllApps (nodeID "") or
// ListAppsByNodeID returns without reading them. Postgres gives every new row
// version a new xmin, so an insert, update or soft delete of any listed app,
// or an app moving to or from the node, changes Count or RowVersions. A
// VACUUM FREEZE can change it too, which only costs one extra read.
type AppsListVersion struct {
	Count       int64
	RowVersions int64
}

// AppsListVersionReader lets a poller that lists apps every second (schedd's
// scale-up, targets and floor triggers, its reaper and scaling-policy
// observer) reread the full rows only when they changed. Each full read
// carries every app's manifest; the version is one row.
type AppsListVersionReader interface {
	AppsListVersion(ctx context.Context, nodeID string) (AppsListVersion, error)
}

const appsListVersionSQL = `select count(*), coalesce(sum(xmin::text::bigint), 0)
  from apps
 where status <> 'deleted'
   and ($1 = '' or node_id = nullif($1, '')::uuid)`

// AppsListVersion reads the version of the apps ListAllApps (nodeID "") or
// ListAppsByNodeID(nodeID) returns.
func (s *PgStore) AppsListVersion(ctx context.Context, nodeID string) (AppsListVersion, error) {
	var v AppsListVersion
	if err := s.pool.QueryRow(ctx, appsListVersionSQL, nodeID).Scan(&v.Count, &v.RowVersions); err != nil {
		return AppsListVersion{}, err
	}
	return v, nil
}
