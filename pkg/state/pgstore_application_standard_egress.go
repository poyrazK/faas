package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardEgressStore = (*PgStore)(nil)

func (s *PgStore) ListPendingApplicationStandardEgress(ctx context.Context, appID string) ([]ApplicationStandardEgressTarget, error) {
	if appID != "" && !validStandardResourceRead(appID, appID) {
		return nil, ErrInvalidArgument
	}
	if appID != "" {
		appID = canonicalStandardUUID(appID)
	}
	rows, err := sqlc.New().ListPendingApplicationStandardEgress(ctx, s.pool, sqlc.ListPendingApplicationStandardEgressParams{AppID: appID, FreshnessSeconds: api.ApplicationStandardEgressFreshness.Seconds() / 2, TargetLimit: api.ApplicationStandardEgressBatchLimit})
	if err != nil {
		return nil, fmt.Errorf("list pending standard egress: %w", err)
	}
	result := []ApplicationStandardEgressTarget{}
	for _, raw := range rows {
		var t ApplicationStandardEgressTarget
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, nil
}

func (s *PgStore) RecordApplicationStandardEgress(ctx context.Context, t ApplicationStandardEgressTarget, r runtimeadmission.EgressReceipt) (ApplicationStandardEgressObservation, error) {
	if !t.valid() || r.Check(t.Identity, t.Policy) != nil {
		return ApplicationStandardEgressObservation{}, ErrInvalidArgument
	}
	target, err := json.Marshal(t)
	if err != nil {
		return ApplicationStandardEgressObservation{}, err
	}
	receipt, err := json.Marshal(r)
	if err != nil {
		return ApplicationStandardEgressObservation{}, err
	}
	raw, err := sqlc.New().RecordApplicationStandardEgress(ctx, s.pool, sqlc.RecordApplicationStandardEgressParams{AppID: mustPgUUID(t.AppID), OrgID: mustPgUUID(t.OrgID), NodeID: mustPgUUID(t.Identity.NodeID), Target: target, Receipt: receipt})
	if err != nil {
		return ApplicationStandardEgressObservation{}, standardEgressPGError(err)
	}
	var o ApplicationStandardEgressObservation
	if err := json.Unmarshal(raw, &o); err != nil {
		return o, err
	}
	return o, nil
}

func standardEgressPGError(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) {
		if p.Code == "55P03" {
			return ErrApplicationStandardReviewBusy
		}
		if p.Code == "40001" || p.ConstraintName == "application_standard_egress_current" {
			return ErrApplicationStandardRuntimeStale
		}
	}
	return err
}

func (s *PgStore) ListApplicationStandardEgress(ctx context.Context, orgID, appID string) ([]ApplicationStandardEgressObservation, error) {
	if !validStandardResourceRead(orgID, appID) {
		return nil, ErrInvalidArgument
	}
	q := sqlc.New()
	if _, err := q.GetApplicationStandardLogDeliveryApp(ctx, s.pool, sqlc.GetApplicationStandardLogDeliveryAppParams{AppID: mustPgUUID(appID), OrgID: mustPgUUID(orgID)}); err != nil {
		return nil, mapErr(err)
	}
	rows, err := q.ListApplicationStandardEgress(ctx, s.pool, sqlc.ListApplicationStandardEgressParams{AppID: mustPgUUID(appID), OrgID: mustPgUUID(orgID), FreshnessSeconds: api.ApplicationStandardEgressFreshness.Seconds()})
	if err != nil {
		return nil, err
	}
	result := []ApplicationStandardEgressObservation{}
	for _, raw := range rows {
		var o ApplicationStandardEgressObservation
		if err := json.Unmarshal(raw, &o); err != nil {
			return nil, err
		}
		result = append(result, o)
	}
	return result, nil
}
