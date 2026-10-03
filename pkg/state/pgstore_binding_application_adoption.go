package state

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ BindingApplicationAdoptionStore = (*PgStore)(nil)

func (s *PgStore) ReadBindingApplicationAdoption(ctx context.Context, accountID, appID string, selectors []BindingAdoptionSelector) ([]BindingApplicationAdoptionRow, error) {
	params := sqlc.ReadBindingApplicationAdoptionParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), PostgresIds: []pgtype.UUID{}, StorageIds: []pgtype.UUID{}}
	for _, selector := range selectors {
		id, err := uuid.Parse(selector.BindingID)
		if err != nil {
			return nil, fmt.Errorf("binding adoption selector: %w", ErrInvalidArgument)
		}
		value := pgtype.UUID{Bytes: id, Valid: true}
		if selector.Type == api.BindingTypePostgres {
			params.PostgresIds = append(params.PostgresIds, value)
		}
		if selector.Type == api.BindingTypeObjectStorage {
			params.StorageIds = append(params.StorageIds, value)
		}
	}
	rows, err := sqlc.New().ReadBindingApplicationAdoption(ctx, s.pool, params)
	if err != nil {
		return nil, fmt.Errorf("read binding application adoption: %w", mapErr(err))
	}
	out := []BindingApplicationAdoptionRow{}
	for _, row := range rows {
		selected := false
		for _, selector := range selectors {
			id, _ := uuid.Parse(selector.BindingID)
			selected = selected || selector.Type == row.BindingType && id == row.BindingID.Bytes && selector.Scope == row.Scope && slices.Contains(selector.Keys, row.Key)
		}
		if !selected {
			continue
		}
		out = append(out, BindingApplicationAdoptionRow{Type: row.BindingType, BindingID: uuidString(row.BindingID), Scope: row.Scope, Key: row.Key,
			CurrentVersion: row.CurrentVersion, DeploymentID: row.DeploymentID, InstanceID: row.InstanceID, WorkloadName: row.WorkloadName,
			RuntimeState: row.RuntimeState, ReloadSupport: row.ReloadSupport, ReloadVersion: row.ReloadVersion, Projection: row.Projection, Signal: row.Signal,
			ReloadAt: optionalHealthTime(row.ObservedAt), ApplicationAckVersion: row.ApplicationAckVersion,
			ApplicationAck: row.ApplicationAckStatus, ApplicationAckAt: optionalHealthTime(row.ApplicationAckAt),
			ProcessGeneration: row.ProcessGeneration, ApplicationAckGeneration: row.ApplicationAckGeneration})
	}
	return out, nil
}
