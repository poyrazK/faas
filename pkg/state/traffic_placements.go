// adr: 570
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type TrafficPlacement struct {
	Region, CommitSHA, DeploymentTag, DeploymentCreatedAt, ImageDigest string
	AppID, InstanceID, DeploymentID, NodeID, WakeID                    string
	DeploymentLive, FunctionHandler                                    bool
	OverridePort                                                       int
	InferredProfile                                                    json.RawMessage
}

// Incomplete snapshots cannot prove absence. The extra sentinel is never
// returned as a routing candidate, and a missing app still has an empty snapshot.
type TrafficPlacementSnapshot struct {
	AppID    string
	Complete bool
	Targets  []TrafficPlacement
}

func (s *PgStore) RunningTrafficPlacements(ctx context.Context, appIDs []string) (map[string]TrafficPlacementSnapshot, error) {
	if len(appIDs) > api.TrafficPlacementAppBatchSize {
		return nil, fmt.Errorf("traffic placement batch exceeds %d apps", api.TrafficPlacementAppBatchSize)
	}
	out := make(map[string]TrafficPlacementSnapshot, len(appIDs))
	if len(appIDs) == 0 {
		return out, nil
	}
	args := sqlc.RunningTrafficPlacementsParams{MaxRows: api.TrafficPlacementTargetsPerApp + 1}
	for _, id := range appIDs {
		var parsed pgtype.UUID
		if err := parsed.Scan(id); err != nil || !parsed.Valid {
			return nil, fmt.Errorf("invalid traffic placement app identity %q", id)
		}
		canonical, _ := parsed.Value()
		if canonical != id {
			return nil, fmt.Errorf("noncanonical traffic placement app identity %q", id)
		}
		if _, exists := out[id]; !exists {
			args.AppIds = append(args.AppIds, parsed)
			out[id] = TrafficPlacementSnapshot{AppID: id, Complete: true}
		}
	}
	rows, err := sqlc.New().RunningTrafficPlacements(ctx, s.pool, args)
	if err != nil {
		return nil, fmt.Errorf("read running traffic placements: %w", err)
	}
	for _, row := range rows {
		snapshot, requested := out[row.AppID]
		if !requested {
			return nil, fmt.Errorf("unexpected traffic placement app identity %q", row.AppID)
		}
		if row.InstanceID == "" {
			continue
		}
		if len(snapshot.Targets) == api.TrafficPlacementTargetsPerApp {
			snapshot.Complete = false
		} else {
			createdAt := ""
			if row.DeploymentCreatedAt.Valid {
				createdAt = row.DeploymentCreatedAt.Time.UTC().Format(time.RFC3339Nano)
			}
			snapshot.Targets = append(snapshot.Targets, TrafficPlacement{AppID: row.AppID,
				InstanceID: row.InstanceID, DeploymentID: row.DeploymentID, NodeID: row.NodeID, WakeID: row.WakeID,
				DeploymentLive: row.DeploymentLive, OverridePort: int(row.OverridePort),
				FunctionHandler: row.FunctionHandler, InferredProfile: row.InferredProfile,
				Region: row.Region, CommitSHA: row.CommitSha, DeploymentTag: row.DeploymentTag,
				DeploymentCreatedAt: createdAt, ImageDigest: row.ImageDigest})
		}
		out[row.AppID] = snapshot
	}
	return out, nil
}
