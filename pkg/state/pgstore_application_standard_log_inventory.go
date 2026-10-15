package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardLogInventoryStore = (*PgStore)(nil)

func (s *PgStore) LoadApplicationStandardLogConsumerSnapshot(ctx context.Context) (ApplicationStandardLogConsumerSnapshot, error) {
	raw, err := sqlc.New().LoadApplicationStandardLogConsumerSnapshot(ctx, s.pool)
	if err != nil {
		return ApplicationStandardLogConsumerSnapshot{}, fmt.Errorf("load standard log consumer snapshot: %w", err)
	}
	var wire struct {
		Drains []struct {
			Spec    AppLogDrain                         `json:"spec"`
			Binding *ApplicationStandardLogDrainBinding `json:"binding"`
		} `json:"drains"`
		Inventories []ApplicationStandardLogInventory `json:"inventories"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return ApplicationStandardLogConsumerSnapshot{}, fmt.Errorf("decode standard log consumer snapshot: %w", err)
	}
	r := ApplicationStandardLogConsumerSnapshot{Drains: []AppLogDrain{}, Inventories: wire.Inventories}
	for _, d := range wire.Drains {
		d.Spec.StandardBinding = d.Binding
		r.Drains = append(r.Drains, d.Spec)
	}
	return r, nil
}

func (s *PgStore) RegisterApplicationStandardLogConsumer(ctx context.Context, nodeID, sessionID string) (ApplicationStandardLogConsumerSession, error) {
	if !validStandardResourceRead(nodeID, sessionID) {
		return ApplicationStandardLogConsumerSession{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplicationStandardLogConsumerSession{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := lockStandardLogConsumerRegistration(ctx, tx, nodeID); err != nil {
		return ApplicationStandardLogConsumerSession{}, err
	}
	raw, err := q.RegisterApplicationStandardLogConsumer(ctx, tx, sqlc.RegisterApplicationStandardLogConsumerParams{NodeID: mustPgUUID(nodeID), SessionID: mustPgUUID(sessionID)})
	if err != nil {
		return ApplicationStandardLogConsumerSession{}, standardLogInventoryPGError(err)
	}
	var result ApplicationStandardLogConsumerSession
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, fmt.Errorf("decode standard log consumer: %w", err)
	}
	return result, tx.Commit(ctx)
}

func lockStandardLogConsumerRegistration(ctx context.Context, tx pgx.Tx, nodeID string) error {
	q := sqlc.New()
	locked, err := q.TryLockApplicationStandardLogConsumer(ctx, tx, mustPgUUID(nodeID))
	if err != nil {
		return err
	}
	if !locked {
		return ErrApplicationStandardReviewBusy
	}
	if _, err := q.LockApplicationStandardLogConsumerNode(ctx, tx, mustPgUUID(nodeID)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrApplicationStandardLogConsumerFenced
		}
		return standardLogInventoryPGError(err)
	}
	_, err = q.LockApplicationStandardLogConsumerRegistration(ctx, tx, mustPgUUID(nodeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return standardLogInventoryPGError(err)
}

func (s *PgStore) CheckApplicationStandardLogConsumer(ctx context.Context, session ApplicationStandardLogConsumerSession) error {
	if !validStandardLogSession(session) {
		return ErrInvalidArgument
	}
	return checkStandardLogConsumer(ctx, s.pool, session)
}

func checkStandardLogConsumer(ctx context.Context, db sqlc.DBTX, s ApplicationStandardLogConsumerSession) error {
	_, err := sqlc.New().CheckApplicationStandardLogConsumer(ctx, db, sqlc.CheckApplicationStandardLogConsumerParams{NodeID: mustPgUUID(s.NodeID), SessionID: mustPgUUID(s.SessionID), Generation: s.Generation})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrApplicationStandardLogConsumerFenced
	}
	return standardLogInventoryPGError(err)
}

func (s *PgStore) RecordApplicationStandardLogInventory(ctx context.Context, session ApplicationStandardLogConsumerSession, i ApplicationStandardLogInventory) (ApplicationStandardLogInventoryObservation, error) {
	if !validStandardLogSession(session) || !validStandardLogInventory(i) {
		return ApplicationStandardLogInventoryObservation{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplicationStandardLogInventoryObservation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := checkStandardLogConsumer(ctx, tx, session); err != nil {
		return ApplicationStandardLogInventoryObservation{}, err
	}
	if _, err := sqlc.New().LockApplicationStandardLogConsumerNode(ctx, tx, mustPgUUID(session.NodeID)); err != nil {
		return ApplicationStandardLogInventoryObservation{}, standardLogInventoryPGError(err)
	}
	if err := lockStandardLogInventory(ctx, tx, i); err != nil {
		return ApplicationStandardLogInventoryObservation{}, err
	}
	raw, err := json.Marshal(i)
	if err != nil {
		return ApplicationStandardLogInventoryObservation{}, err
	}
	result, err := sqlc.New().RecordApplicationStandardLogInventory(ctx, tx, sqlc.RecordApplicationStandardLogInventoryParams{AppID: mustPgUUID(i.AppID), OrgID: mustPgUUID(i.OrgID), NodeID: mustPgUUID(session.NodeID), SessionID: mustPgUUID(session.SessionID), Generation: session.Generation, Inventory: raw})
	if err != nil {
		return ApplicationStandardLogInventoryObservation{}, standardLogInventoryPGError(err)
	}
	var o ApplicationStandardLogInventoryObservation
	if err := json.Unmarshal(result, &o); err != nil {
		return o, fmt.Errorf("decode standard log inventory observation: %w", err)
	}
	return o, tx.Commit(ctx)
}

func lockStandardLogInventory(ctx context.Context, tx pgx.Tx, i ApplicationStandardLogInventory) error {
	q := sqlc.New()
	locked, err := q.TryLockApplicationStandardApprovalControls(ctx, tx, []pgtype.UUID{mustPgUUID(i.AppID)})
	if err != nil {
		return err
	}
	if !locked {
		return ErrApplicationStandardReviewBusy
	}
	_, err = q.LockApplicationStandardLogInventoryParents(ctx, tx, sqlc.LockApplicationStandardLogInventoryParentsParams{AppID: mustPgUUID(i.AppID), OrgID: mustPgUUID(i.OrgID)})
	return standardLogInventoryPGError(err)
}

func standardLogInventoryPGError(err error) error {
	if err == nil {
		return nil
	}
	var p *pgconn.PgError
	if errors.As(err, &p) && p.Code == "55000" {
		return ErrApplicationStandardLogConsumerFenced
	}
	if errors.As(err, &p) && p.Code == "55P03" {
		return ErrApplicationStandardReviewBusy
	}
	return standardLogDeliveryPGError(err)
}

func (s *PgStore) ListApplicationStandardLogInventories(ctx context.Context, orgID, appID string) ([]ApplicationStandardLogInventoryObservation, error) {
	if !validStandardResourceRead(orgID, appID) {
		return nil, ErrInvalidArgument
	}
	q := sqlc.New()
	if _, err := q.GetApplicationStandardLogDeliveryApp(ctx, s.pool, sqlc.GetApplicationStandardLogDeliveryAppParams{AppID: mustPgUUID(appID), OrgID: mustPgUUID(orgID)}); err != nil {
		return nil, mapErr(err)
	}
	rows, err := q.ListApplicationStandardLogInventories(ctx, s.pool, sqlc.ListApplicationStandardLogInventoriesParams{AppID: mustPgUUID(appID), OrgID: mustPgUUID(orgID), FreshnessSeconds: api.ApplicationStandardLogInventoryFreshness.Seconds()})
	if err != nil {
		return nil, fmt.Errorf("list standard log inventories: %w", err)
	}
	result := []ApplicationStandardLogInventoryObservation{}
	for _, raw := range rows {
		var o ApplicationStandardLogInventoryObservation
		if err := json.Unmarshal(raw, &o); err != nil {
			return nil, fmt.Errorf("decode standard log inventories: %w", err)
		}
		result = append(result, o)
	}
	return result, nil
}
