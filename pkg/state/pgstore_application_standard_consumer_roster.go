package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardConsumerRosterStore = (*PgStore)(nil)

func (s *PgStore) GetApplicationStandardConsumerRoster(ctx context.Context, orgID, appID string) (ApplicationStandardConsumerRoster, error) {
	return readStandardConsumerRoster(ctx, s.pool, orgID, appID)
}

func readStandardConsumerRoster(ctx context.Context, db sqlc.DBTX, orgID, appID string) (ApplicationStandardConsumerRoster, error) {
	if !validStandardResourceRead(orgID, appID) {
		return ApplicationStandardConsumerRoster{}, ErrInvalidArgument
	}
	raw, err := sqlc.New().GetApplicationStandardConsumerRoster(ctx, db, sqlc.GetApplicationStandardConsumerRosterParams{AppID: mustPgUUID(appID), OrgID: mustPgUUID(orgID), HeartbeatFreshnessSeconds: DefaultHeartbeatStaleness.Seconds()})
	if err != nil {
		return ApplicationStandardConsumerRoster{}, mapErr(err)
	}
	var r ApplicationStandardConsumerRoster
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, fmt.Errorf("decode application standard consumer roster: %w", err)
	}
	return r, nil
}
