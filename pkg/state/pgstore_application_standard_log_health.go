package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardLogHealthStore = (*PgStore)(nil)

func (s *PgStore) RecordApplicationStandardLogHealth(ctx context.Context, session ApplicationStandardLogConsumerSession, d AppLogDrain, e ApplicationStandardLogHealthEvent) (ApplicationStandardLogHealthObservation, error) {
	if !validStandardLogHealth(session, d, e) {
		return ApplicationStandardLogHealthObservation{}, ErrInvalidArgument
	}
	b := d.StandardBinding
	binding, err := json.Marshal(b)
	if err != nil {
		return ApplicationStandardLogHealthObservation{}, err
	}
	var source pgtype.UUID
	if e.SourceInstanceID != "" {
		source = mustPgUUID(e.SourceInstanceID)
	}
	raw, err := sqlc.New().RecordApplicationStandardLogHealth(ctx, s.pool, sqlc.RecordApplicationStandardLogHealthParams{
		AppID: mustPgUUID(b.AppID), OrgID: mustPgUUID(b.OrgID), DrainID: mustPgUUID(b.DrainID), NodeID: mustPgUUID(session.NodeID), SessionID: mustPgUUID(session.SessionID), Generation: session.Generation,
		Binding: binding, EventRevision: e.EventRevision, Status: e.Status, Reason: e.Reason, SourceInstanceID: source, Sequence: e.Sequence,
	})
	if err != nil {
		return ApplicationStandardLogHealthObservation{}, standardLogHealthPGError(err)
	}
	var result ApplicationStandardLogHealthObservation
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, fmt.Errorf("decode standard log health: %w", err)
	}
	return result, nil
}

func standardLogHealthPGError(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) && p.Code == "GS001" {
		return ErrApplicationStandardLogHealthStale
	}
	return standardLogInventoryPGError(err)
}

func (s *PgStore) ListApplicationStandardLogHealth(ctx context.Context, orgID, appID string) ([]ApplicationStandardLogHealthObservation, error) {
	if !validStandardResourceRead(orgID, appID) {
		return nil, ErrInvalidArgument
	}
	q := sqlc.New()
	if _, err := q.GetApplicationStandardLogDeliveryApp(ctx, s.pool, sqlc.GetApplicationStandardLogDeliveryAppParams{AppID: mustPgUUID(appID), OrgID: mustPgUUID(orgID)}); err != nil {
		return nil, mapErr(err)
	}
	rows, err := q.ListApplicationStandardLogHealth(ctx, s.pool, sqlc.ListApplicationStandardLogHealthParams{AppID: mustPgUUID(appID), OrgID: mustPgUUID(orgID), FreshnessSeconds: api.ApplicationStandardLogHealthFreshness.Seconds()})
	if err != nil {
		return nil, fmt.Errorf("list standard log health: %w", err)
	}
	result := []ApplicationStandardLogHealthObservation{}
	for _, raw := range rows {
		var row ApplicationStandardLogHealthObservation
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, fmt.Errorf("decode standard log health: %w", err)
		}
		result = append(result, row)
	}
	return result, nil
}
