package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func persistTaskRetirement(ctx context.Context, tx pgx.Tx, r api.TaskToolReleaseIntentRecord) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return toolpublicationdb.New().UpdateTaskToolReleaseIntent(ctx, tx, toolpublicationdb.UpdateTaskToolReleaseIntentParams{AccountID: mustPgUUID(r.AccountID), ID: r.ID, Status: r.Status, Payload: b})
}
func (s *PgStore) changeTaskRetirementTx(ctx context.Context, tx pgx.Tx, account string, r api.TaskToolReleaseIntentRecord, in api.TaskToolReleaseIntentDecision) (api.TaskToolReleaseIntentRecord, error) {
	if in.Retirement == nil || !tasktoolrelease.ValidRetirement(r) {
		return r, ErrConflict
	}
	actor := r.RequesterKeyID
	owner := retirementOwner(in)
	if owner {
		actor = in.AuthorizerKeyID
	}
	fresh, err := taskToolReleaseFresh(ctx, tx, account, actor, api.TaskToolReleaseIntent{}, false)
	if err != nil || !fresh {
		return r, ErrConflict
	}
	if owner {
		if _, err := toolpublicationdb.New().LockTaskToolExecutionOwnerKey(ctx, tx, toolpublicationdb.LockTaskToolExecutionOwnerKeyParams{AccountID: mustPgUUID(account), ID: mustPgUUID(actor)}); err != nil {
			return r, ErrConflict
		}
	}
	if in.Decision == "recovery_retire_qualify" || in.Decision == "recovery_retire_approve" {
		apps := retirementApps(r, in)
		fresh, err = taskToolReleaseFresh(ctx, tx, account, actor, api.TaskToolReleaseIntent{Apps: apps}, false)
		if err != nil || len(apps) == 0 {
			return r, ErrConflict
		}
	}
	if !owner {
		if in.Retirement.Action != "observe" {
			return r, ErrConflict
		}
		fresh, err = s.retirementDrained(ctx, tx, r)
		if err != nil {
			return r, err
		}
	}
	out, err := tasktoolrelease.Retirement(r, in, fresh, time.Now(), "")
	if err != nil {
		return r, ErrConflict
	}
	if err = persistTaskRetirement(ctx, tx, out); err != nil {
		return r, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) prepareTaskRetirementPark(ctx context.Context, tx pgx.Tx, app, dep string) (*api.TaskToolReleaseIntentRecord, error) {
	g := retirementGuardFrom(ctx)
	if g == nil {
		return nil, nil
	}
	q := toolpublicationdb.New()
	b, err := q.LockTaskToolReleaseIntentNowait(ctx, tx, toolpublicationdb.LockTaskToolReleaseIntentNowaitParams{AccountID: mustPgUUID(g.account), ID: g.intent})
	if err != nil {
		return nil, ErrConflict
	}
	var r api.TaskToolReleaseIntentRecord
	if json.Unmarshal(b, &r) != nil || !retirementTarget(r, g, app, dep) || r.Execution.Recovery.Retirement == nil {
		return nil, ErrConflict
	}
	retire := r.Execution.Recovery.Retirement
	fresh, err := taskToolReleaseFresh(ctx, tx, g.account, g.key, api.TaskToolReleaseIntent{Apps: retire.Qualification.NativeApps}, false)
	if err != nil || !fresh {
		return nil, ErrConflict
	}
	fresh, err = s.taskCleanupRuntime(ctx, tx, r, retirementEvidence(&g.request), retire.Qualification.NativeApps, retire.AuthorizerKeyID, retire.Qualification.ExpiresAt)
	if err != nil || !fresh {
		return nil, ErrConflict
	}
	actual, err := q.ReadTaskRetirementApp(ctx, tx, toolpublicationdb.ReadTaskRetirementAppParams{AccountID: mustPgUUID(g.account), ID: mustPgUUID(app)})
	var manifest AppManifest
	if err != nil || json.Unmarshal(actual.Manifest, &manifest) != nil || !retirementWorkerShape(manifest, actual.WorkloadClass, g.request.ShutdownTimeoutMS) {
		return nil, ErrConflict
	}
	out, err := tasktoolrelease.Retirement(r, api.TaskToolReleaseIntentDecision{ReleaseFingerprint: r.Intent.ReleaseFingerprint, Retirement: &g.request}, true, time.Now(), uuid.NewString())
	if err != nil {
		return nil, ErrConflict
	}
	return &out, nil
}
func (s *PgStore) completeTaskRetirementPark(ctx context.Context, tx pgx.Tx, r *api.TaskToolReleaseIntentRecord) error {
	if r == nil {
		return nil
	}
	q := r.Execution.Recovery.Retirement
	fresh, err := toolpublicationdb.New().TaskQuarantineClockFresh(ctx, tx, toolpublicationdb.TaskQuarantineClockFreshParams{AccountID: mustPgUUID(r.AccountID), OwnerKeyID: mustPgUUID(q.AuthorizerKeyID), RequesterKeyID: mustPgUUID(r.RequesterKeyID), ExpiresAt: pgtype.Timestamptz{Time: q.Qualification.ExpiresAt, Valid: true}})
	if err != nil || !fresh {
		return ErrConflict
	}
	for _, pin := range q.Qualification.NativeApps {
		if pin.App == r.Intent.ObserverMetricApp {
			metric, err := toolpublicationdb.New().LockTaskRecoveryHeartbeat(ctx, tx, mustPgUUID(pin.Review.AppID))
			if err != nil || !metric.ObservedAt.Valid || !taskRecoveryHeartbeatOK(metric.Value, metric.ObservedAt.Time, time.Now()) {
				return ErrConflict
			}
		}
	}
	return persistTaskRetirement(ctx, tx, *r)
}
func (s *PgStore) retirementDrained(ctx context.Context, tx pgx.Tx, r api.TaskToolReleaseIntentRecord) (bool, error) {
	if r.Execution == nil || r.Execution.Recovery == nil || r.Execution.Recovery.Retirement == nil {
		return false, nil
	}
	id := retirementWorker(r)
	q := toolpublicationdb.New()
	if _, err := q.LockTaskRecoveryApp(ctx, tx, toolpublicationdb.LockTaskRecoveryAppParams{AccountID: mustPgUUID(r.AccountID), ID: mustPgUUID(id)}); err != nil {
		return false, err
	}
	app, err := q.ReadTaskRetirementApp(ctx, tx, toolpublicationdb.ReadTaskRetirementAppParams{AccountID: mustPgUUID(r.AccountID), ID: mustPgUUID(id)})
	if err != nil {
		return false, err
	}
	if app.Status != string(AppEvictedCold) || app.ParkTransitionID != r.Execution.Recovery.Retirement.ParkTransitionID {
		return false, nil
	}
	latest, err := q.ReadTaskRetirementLatestDeployment(ctx, tx, mustPgUUID(id))
	if err != nil || !sameDeploymentID(latest, r.Execution.Receipt.WorkerDeploymentID) {
		return false, err
	}
	rows, err := q.LockTaskRecoveryInstances(ctx, tx, mustPgUUID(id))
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if IsLive(row.State) {
			return false, nil
		}
	}
	return true, nil
}
