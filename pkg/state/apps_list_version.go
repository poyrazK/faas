package state

import (
	"context"
	"time"
)

// AppsListVersion identifies the rows ListAllApps (nodeID "") or
// ListAppsByNodeID returns without reading them (ADR 20261103094355).
//
// Generations is the sum of the listed apps' runtime generations
// (app_runtime_generations), which the apps trigger bumps on every change to
// a row except to the columns it skips: the scale stamps last_scale_in_at and
// last_scale_out_at, last_deploy_failed_email_at, purge_claimed_at and
// preview_destroy_commented_at. Every wake and idle park writes a scale stamp,
// so a version over every row version (the earlier xmin sum) moved most
// seconds on a busy node and the lists were reread every time. A generation
// only grows, so with the same members any other change moves the sum.
// Count and Members (a sum of the listed IDs' hashes) move when an app joins
// or leaves the list, by insert, delete, soft delete or a node move.
type AppsListVersion struct {
	Count       int64
	Generations int64
	Members     int64
}

// AppsListVersionReader lets a poller that lists apps every second (schedd's
// scale-up, targets and floor triggers, its reaper and scaling-policy
// observer) reread the full rows only when they changed. Each full read
// carries every app's manifest; the version is one row. The columns the
// generation skips can be older in a reused list than in the row: a caller
// acting on a scale stamp reads it fresh (AppScaleOutStampReader).
type AppsListVersionReader interface {
	AppsListVersion(ctx context.Context, nodeID string) (AppsListVersion, error)
}

// AppScaleOutStampReader reads an app's last_scale_out_at as the row holds it
// now. The scale-out triggers read it before admitting, because a reused app
// list can carry an older stamp, which would end a cooldown early.
type AppScaleOutStampReader interface {
	AppScaleOutStamp(ctx context.Context, appID string) (*time.Time, error)
}

const appsListVersionSQL = `select count(*),
       coalesce(sum(coalesce(g.generation, 0)), 0)::bigint,
       coalesce(sum(hashtext(a.id::text)), 0)::bigint
  from apps a
  left join app_runtime_generations g on g.app_id = a.id
 where a.status <> 'deleted'
   and ($1 = '' or a.node_id = nullif($1, '')::uuid)`

// AppsListVersion reads the version of the apps ListAllApps (nodeID "") or
// ListAppsByNodeID(nodeID) returns.
func (s *PgStore) AppsListVersion(ctx context.Context, nodeID string) (AppsListVersion, error) {
	var v AppsListVersion
	if err := s.pool.QueryRow(ctx, appsListVersionSQL, nodeID).Scan(&v.Count, &v.Generations, &v.Members); err != nil {
		return AppsListVersion{}, err
	}
	return v, nil
}
