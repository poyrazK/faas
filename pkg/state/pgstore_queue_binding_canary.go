// adr:1352
package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

func queueCanarySnapshotDB(ctx context.Context, db sqlc.DBTX, account, app, deployment, binding string) (queueCanarySnapshot, error) {
	raw, err := sqlc.New().QueueCanarySnapshot(ctx, db, sqlc.QueueCanarySnapshotParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), DeploymentID: mustPgUUID(deployment), Binding: binding})
	var s queueCanarySnapshot
	if err != nil {
		return s, mapErr(err)
	}
	err = json.Unmarshal(raw, &s)
	return s, err
}
func (s *PgStore) CreateQueueBindingCanary(ctx context.Context, account, app string, req api.CreateQueueBindingCanaryRequest) (api.QueueBindingCanary, error) {
	if err := validateQueueCanaryRequest(req); err != nil {
		return api.QueueBindingCanary{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.QueueBindingCanary{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.LockQueueCanaryApp(ctx, tx, sqlc.LockQueueCanaryAppParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)}); err != nil {
		return api.QueueBindingCanary{}, mapErr(err)
	}
	r, err := createQueueBindingCanaryTx(ctx, tx, account, app, req)
	if err != nil {
		return api.QueueBindingCanary{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return api.QueueBindingCanary{}, mapErr(err)
	}
	return r, nil
}
func createQueueBindingCanaryTx(ctx context.Context, tx pgx.Tx, account, app string, req api.CreateQueueBindingCanaryRequest) (api.QueueBindingCanary, error) {
	q := sqlc.New()
	snap, err := queueCanarySnapshotDB(ctx, tx, account, app, req.DeploymentID, req.Binding)
	if err != nil {
		return api.QueueBindingCanary{}, err
	}
	if !queueCanarySupported(snap) {
		return api.QueueBindingCanary{}, ErrInvalidArgument
	}
	accountRow, err := q.AccountByID(ctx, tx, mustPgUUID(account))
	if err != nil {
		return api.QueueBindingCanary{}, mapErr(err)
	}
	depth, err := q.CountProductionPendingInvocations(ctx, tx, sqlc.CountProductionPendingInvocationsParams{AppID: mustPgUUID(app), Source: string(InvocationQueue)})
	if err != nil {
		return api.QueueBindingCanary{}, mapErr(err)
	}
	if err := queueCanaryProducerCapacity(api.Plan(accountRow.Plan), depth); err != nil {
		return api.QueueBindingCanary{}, err
	}
	exists, err := q.ActiveQueueCanaryExists(ctx, tx, sqlc.ActiveQueueCanaryExistsParams{AppID: mustPgUUID(app), BindingID: mustPgUUID(snap.BindingID)})
	if err != nil {
		return api.QueueBindingCanary{}, mapErr(err)
	}
	if exists {
		return api.QueueBindingCanary{}, ErrConflict
	}
	r, inv := newQueueCanary(snap, req, account, app)
	if _, err = enqueueInvocationRow(ctx, tx, inv); err != nil {
		return api.QueueBindingCanary{}, err
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return api.QueueBindingCanary{}, err
	}
	err = q.InsertQueueCanary(ctx, tx, sqlc.InsertQueueCanaryParams{ID: mustPgUUID(inv.ID), AccountID: mustPgUUID(account), AppID: mustPgUUID(app), BindingID: mustPgUUID(snap.BindingID),
		TriggerID: mustPgUUID(snap.TriggerID), DeploymentID: mustPgUUID(snap.DeploymentID), Snapshot: raw, PublishedAt: nullableTimestamptzPtr(&r.Report.PublishedAt), ExpiresAt: nullableTimestamptzPtr(&r.Report.ExpiresAt)})
	if err != nil {
		return api.QueueBindingCanary{}, mapErr(err)
	}
	return r.Report, nil
}

func (s *PgStore) GetQueueBindingCanary(ctx context.Context, account, app, id string) (api.QueueBindingCanary, error) {
	if !canonicalStateUUID(id) {
		return api.QueueBindingCanary{}, ErrInvalidArgument
	}
	row, err := sqlc.New().ReadQueueCanary(ctx, s.pool, sqlc.ReadQueueCanaryParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), ID: mustPgUUID(id)})
	if err != nil {
		return api.QueueBindingCanary{}, mapErr(err)
	}
	var snap queueCanarySnapshot
	if err = json.Unmarshal(row.Snapshot, &snap); err != nil {
		return api.QueueBindingCanary{}, err
	}
	r := queueCanaryRecord{Snapshot: snap, Cancelled: row.Cancelled, CompletedDeploymentID: pgUUIDString(row.CompletedDeploymentID), CompletedAttempt: int(row.CompletedAttempt.Int32), CompletedGeneration: row.CompletedGeneration.Int64,
		Report: api.QueueBindingCanary{ID: id, Binding: snap.Name, DeploymentID: snap.DeploymentID, Scope: snap.Scope, PublishedAt: row.PublishedAt.Time, ExpiresAt: row.ExpiresAt.Time}}
	inv := Invocation{State: InvocationState(row.InvocationState), Attempts: int(row.Attempts), ReplayGeneration: row.ReplayGeneration, ReceivedAt: invocationTimePointer(row.ReceivedAt), CompletedAt: invocationTimePointer(row.CompletedAt)}
	current, err := queueCanarySnapshotDB(ctx, s.pool, account, app, snap.DeploymentID, snap.Name)
	if errors.Is(err, ErrNotFound) {
		return queueCanaryStatus(r, inv, nil, time.Now()), nil
	}
	if err != nil {
		return api.QueueBindingCanary{}, err
	}
	return queueCanaryStatus(r, inv, &current, time.Now()), nil
}
func (s *PgStore) CancelQueueBindingCanary(ctx context.Context, account, app, id string) (api.QueueBindingCanary, error) {
	if _, err := s.GetQueueBindingCanary(ctx, account, app, id); err != nil {
		return api.QueueBindingCanary{}, err
	}
	err := sqlc.New().CancelQueueCanary(ctx, s.pool, sqlc.CancelQueueCanaryParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), ID: mustPgUUID(id)})
	if err != nil {
		return api.QueueBindingCanary{}, mapErr(err)
	}
	// Cancellation of in-flight work is cooperative; its evidence is already revoked.
	if err = s.CancelInvocation(ctx, id); err != nil && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) {
		return api.QueueBindingCanary{}, err
	}
	return s.GetQueueBindingCanary(ctx, account, app, id)
}
func (s *PgStore) LatestQueueBindingCanary(ctx context.Context, account, app, deployment, binding string) (api.QueueBindingCanary, error) {
	id, err := sqlc.New().LatestQueueCanaryID(ctx, s.pool, sqlc.LatestQueueCanaryIDParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), DeploymentID: mustPgUUID(deployment), Binding: binding})
	if err != nil {
		return api.QueueBindingCanary{}, mapErr(err)
	}
	return s.GetQueueBindingCanary(ctx, account, app, id)
}

// The dispatch gate reads persisted identity, never a customer marker.
func validateQueueCanaryDispatch(ctx context.Context, store invocationAppReader, app App, inv Invocation, required bool) error {
	canaries, ok := store.(QueueBindingCanaryStore)
	if !ok || !canonicalStateUUID(inv.ID) {
		if required {
			return ErrConflict
		}
		return nil
	}
	r, err := canaries.GetQueueBindingCanary(ctx, app.AccountID, app.ID, inv.ID)
	if errors.Is(err, ErrNotFound) && !required {
		return nil
	}
	if err != nil {
		return err
	}
	if r.Status != "dispatching" || inv.Attempts != 1 || inv.ReplayGeneration != 0 {
		return ErrConflict
	}
	var headers map[string]string
	if json.Unmarshal(inv.Headers, &headers) != nil {
		return ErrConflict
	}
	pin, release, err := invocationPinHeaders(headers)
	if err != nil || pin != r.DeploymentID || release != "" {
		return ErrConflict
	}
	return nil
}
