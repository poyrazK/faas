package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardLogDeliveryStore = (*PgStore)(nil)

func (s *PgStore) RecordApplicationStandardLogDelivery(ctx context.Context, d AppLogDrain, source string, seq uint64) (ApplicationStandardLogDeliveryObservation, error) {
	if !validStandardLogDelivery(d, source, seq) {
		return ApplicationStandardLogDeliveryObservation{}, ErrInvalidArgument
	}
	b := d.StandardBinding
	raw, err := sqlc.New().RecordApplicationStandardLogDelivery(ctx, s.pool, sqlc.RecordApplicationStandardLogDeliveryParams{
		SourceInstanceID: mustPgUUID(source), DrainID: mustPgUUID(b.DrainID), AppID: mustPgUUID(b.AppID), OrgID: mustPgUUID(b.OrgID), ResourceID: mustPgUUID(b.ResourceID),
		DesiredRevision: b.DesiredRevision, EffectiveHash: b.EffectiveHash, ResourceConfigHash: b.ResourceConfigHash, DrainConfigHash: b.DrainConfigHash, Sequence: int64(seq),
	})
	if err != nil {
		return ApplicationStandardLogDeliveryObservation{}, standardLogDeliveryPGError(err)
	}
	var result ApplicationStandardLogDeliveryObservation
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, fmt.Errorf("decode standard log delivery: %w", err)
	}
	return result, nil
}

func standardLogDeliveryPGError(err error) error {
	var p *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) || errors.As(err, &p) && p.Code == "40001" {
		return ErrApplicationStandardLogDeliveryStale
	}
	return standardLogDeliveryError(err)
}

func (s *PgStore) ListApplicationStandardLogDeliveries(ctx context.Context, orgID, appID string) ([]ApplicationStandardLogDeliveryObservation, error) {
	if !validStandardResourceRead(orgID, appID) {
		return nil, ErrInvalidArgument
	}
	q := sqlc.New()
	if _, err := q.GetApplicationStandardLogDeliveryApp(ctx, s.pool, sqlc.GetApplicationStandardLogDeliveryAppParams{AppID: mustPgUUID(appID), OrgID: mustPgUUID(orgID)}); err != nil {
		return nil, mapErr(err)
	}
	rows, err := q.ListApplicationStandardLogDeliveries(ctx, s.pool, sqlc.ListApplicationStandardLogDeliveriesParams{AppID: mustPgUUID(appID), OrgID: mustPgUUID(orgID)})
	if err != nil {
		return nil, fmt.Errorf("list standard log deliveries: %w", err)
	}
	result := []ApplicationStandardLogDeliveryObservation{}
	for _, raw := range rows {
		var row ApplicationStandardLogDeliveryObservation
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, fmt.Errorf("decode standard log deliveries: %w", err)
		}
		result = append(result, row)
	}
	return result, nil
}

func appLogDrainWithStandardBinding(row sqlc.ListEnabledAppLogDrainsWithStandardBindingRow) (AppLogDrain, error) {
	d := AppLogDrain{ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), AccountID: pgUUIDString(row.AccountID), Kind: AppLogDrainKind(row.Kind), TargetURL: row.TargetUrl,
		AuthHeaderSealed: row.AuthHeaderSealed, Enabled: row.Enabled, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
	if err := json.Unmarshal(row.StandardBinding, &d.StandardBinding); err != nil {
		return AppLogDrain{}, fmt.Errorf("decode standard log binding: %w", err)
	}
	return d, nil
}
