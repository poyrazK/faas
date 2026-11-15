package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func (s *PgStore) changeTaskQuarantineTx(ctx context.Context, tx pgx.Tx, account string, r api.TaskToolReleaseIntentRecord, in api.TaskToolReleaseIntentDecision) (api.TaskToolReleaseIntentRecord, error) {
	if !tasktoolrelease.ValidRecoveryResult(r) || in.Quarantine == nil {
		return r, ErrConflict
	}
	actor, owner := quarantineActor(r, in)
	fresh, err := taskToolReleaseFresh(ctx, tx, account, actor, api.TaskToolReleaseIntent{}, false)
	if err != nil || !fresh {
		return r, ErrConflict
	}
	if owner {
		if _, err := toolpublicationdb.New().LockTaskToolExecutionOwnerKey(ctx, tx, toolpublicationdb.LockTaskToolExecutionOwnerKeyParams{AccountID: mustPgUUID(account), ID: mustPgUUID(actor)}); err != nil {
			return r, ErrConflict
		}
	}
	if quarantineNeedsSnapshot(in) {
		apps := quarantineSnapshot(r, in)
		fresh, err = taskToolReleaseFresh(ctx, tx, account, actor, api.TaskToolReleaseIntent{Apps: apps}, false)
		if err != nil || len(apps) == 0 {
			return r, ErrConflict
		}
		if fresh && !owner && in.Quarantine != nil && in.Quarantine.Action == "claim" {
			fresh, err = s.taskQuarantineRuntime(ctx, tx, r, in.Quarantine, apps)
			if err != nil {
				return r, err
			}
		}
	}
	out, err := tasktoolrelease.Quarantine(r, in, fresh, time.Now())
	if err != nil {
		return r, ErrConflict
	}
	body, err := json.Marshal(out)
	if err != nil {
		return r, err
	}
	if err = toolpublicationdb.New().UpdateTaskToolReleaseIntent(ctx, tx, toolpublicationdb.UpdateTaskToolReleaseIntentParams{AccountID: mustPgUUID(account), ID: r.ID, Status: out.Status, Payload: body}); err != nil {
		return r, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) taskQuarantineRuntime(ctx context.Context, tx pgx.Tx, r api.TaskToolReleaseIntentRecord, in *api.TaskToolQuarantineRequest, apps []api.TaskToolReleaseApp) (bool, error) {
	if r.Execution == nil || r.Execution.Recovery == nil || r.Execution.Recovery.Quarantine == nil || in.ExpectedMCPAccessPolicy == nil {
		return false, nil
	}
	q := toolpublicationdb.New()
	owner := r.Execution.Recovery.Quarantine.AuthorizerKeyID
	if _, err := q.LockTaskToolExecutionOwnerKey(ctx, tx, toolpublicationdb.LockTaskToolExecutionOwnerKeyParams{AccountID: mustPgUUID(r.AccountID), ID: mustPgUUID(owner)}); err != nil {
		return false, nil
	}
	metricApp := ""
	for _, pin := range apps {
		if _, err := q.LockTaskRecoveryApp(ctx, tx, toolpublicationdb.LockTaskRecoveryAppParams{AccountID: mustPgUUID(r.AccountID), ID: mustPgUUID(pin.Review.AppID)}); err != nil {
			return false, nil
		}
		rows, err := q.LockTaskRecoveryInstances(ctx, tx, mustPgUUID(pin.Review.AppID))
		if err != nil {
			return false, err
		}
		instances := make([]api.InstanceResponse, 0, len(rows))
		for _, row := range rows {
			instances = append(instances, api.InstanceResponse{ID: row.ID, AppID: row.AppID, DeploymentID: row.DeploymentID, State: row.State})
		}
		if !taskRecoveryInstancesOK(r, pin, instances, in.ExpectedInstanceIDs[pin.Review.AppID]) {
			return false, nil
		}
		if pin.App == r.Intent.ObserverMetricApp {
			metricApp = pin.Review.AppID
		}
		if pin.Role == "web" {
			policy := WithMCPAccessPolicyExpectation(ctx, r.AccountID, pin.Review.AppID, *in.ExpectedMCPAccessPolicy)
			if err := s.checkMCPAccessPolicy(policy, tx, pin.Review.AppID); err != nil {
				return false, nil
			}
		}
	}
	metric, err := q.LockTaskRecoveryHeartbeat(ctx, tx, mustPgUUID(metricApp))
	if err != nil || !metric.ObservedAt.Valid || !taskRecoveryHeartbeatOK(metric.Value, metric.ObservedAt.Time, time.Now()) {
		return false, nil
	}
	return q.TaskQuarantineClockFresh(ctx, tx, toolpublicationdb.TaskQuarantineClockFreshParams{AccountID: mustPgUUID(r.AccountID), OwnerKeyID: mustPgUUID(owner), RequesterKeyID: mustPgUUID(r.RequesterKeyID), ExpiresAt: pgtype.Timestamptz{Time: r.Execution.Recovery.Quarantine.Qualification.ExpiresAt, Valid: true}})
}
