// adr: 531
package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RunningDeploymentSmokeTarget reads one exact candidate without scanning
// terminal history or publishing an unpromoted deployment as customer traffic.
func (s *PgStore) RunningDeploymentSmokeTarget(ctx context.Context, appID, deploymentID string) (TrafficPlacement, bool, error) {
	if err := ctx.Err(); err != nil {
		return TrafficPlacement{}, false, err
	}
	args := sqlc.RunningDeploymentSmokeTargetParams{}
	for _, field := range []struct {
		name, id string
		value    *pgtype.UUID
	}{{"app", appID, &args.AppID}, {"deployment", deploymentID, &args.DeploymentID}} {
		if err := field.value.Scan(field.id); err != nil || !field.value.Valid {
			return TrafficPlacement{}, false, fmt.Errorf("invalid deployment smoke %s identity %q", field.name, field.id)
		}
		canonical, _ := field.value.Value()
		if canonical != field.id {
			return TrafficPlacement{}, false, fmt.Errorf("noncanonical deployment smoke %s identity %q", field.name, field.id)
		}
	}
	row, err := sqlc.New().RunningDeploymentSmokeTarget(ctx, s.pool, args)
	if errors.Is(err, pgx.ErrNoRows) {
		return TrafficPlacement{}, false, nil
	}
	if err != nil {
		return TrafficPlacement{}, false, fmt.Errorf("read deployment smoke placement: %w", err)
	}
	createdAt := ""
	if row.DeploymentCreatedAt.Valid {
		createdAt = row.DeploymentCreatedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	return TrafficPlacement{AppID: row.AppID, InstanceID: row.InstanceID, DeploymentID: row.DeploymentID,
		NodeID: row.NodeID, WakeID: row.WakeID, DeploymentLive: row.DeploymentLive,
		OverridePort: int(row.OverridePort), FunctionHandler: row.FunctionHandler, InferredProfile: row.InferredProfile,
		Region: row.Region, CommitSHA: row.CommitSha, DeploymentTag: row.DeploymentTag,
		DeploymentCreatedAt: createdAt, ImageDigest: row.ImageDigest}, true, nil
}
