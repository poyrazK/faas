package state

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsPreparationStore = (*PgStore)(nil)

func (s *PgStore) PrepareEnvironmentGitOpsImageCandidates(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]EnvironmentWorkloadCandidate, error) {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockEnvironmentGitOpsCandidateApps(ctx, tx, mustPgUUID(lease.Source.ID)); err != nil {
		return nil, mapErr(err)
	}
	source, revision, desired, err := lockApprovedEnvironmentGitOps(ctx, tx, lease.Source.AccountID, lease.Source.ID)
	if err != nil {
		return nil, err
	}
	observed, snapshot, err := readEnvironmentGitOpsIntent(ctx, tx, source, desired)
	if err != nil {
		return nil, err
	}
	plan, err := environmentGitOpsPlan(source, revision, desired, observed, false)
	if err != nil || plan.Hash != reviewed.Hash {
		return nil, ErrConflict
	}
	inputs, err := imageCandidateInputs(source, revision, desired, snapshot, plan)
	if err != nil {
		return nil, err
	}
	if _, err := q.SetEnvironmentGitOpsLeaseContext(ctx, tx, lease.LeaseToken); err != nil {
		return nil, mapErr(err)
	}
	out := make([]EnvironmentWorkloadCandidate, 0, len(inputs))
	for _, input := range inputs {
		frozen, _ := input.ScopedWorkloadRuntime()
		id, err := q.EnvironmentGitOpsCandidateByInput(ctx, tx, sqlc.EnvironmentGitOpsCandidateByInputParams{
			SourceID: lease.Source.ID, Generation: strconv.FormatInt(lease.Source.Generation, 10), Resource: frozen.Resource, PlanHash: plan.Hash})
		if errors.Is(err, pgx.ErrNoRows) {
			id, err = q.CreateEnvironmentGitOpsImageCandidate(ctx, tx, sqlc.CreateEnvironmentGitOpsImageCandidateParams{
				AppID: mustPgUUID(input.AppID), Scope: input.Scope, Image: input.ImageDigest, CommitSha: input.CommitSHA, Runtime: []byte(input.EnvironmentWorkloadRuntime)})
			if err == nil {
				payload, _ := json.Marshal(map[string]string{"app_id": input.AppID, "to": pgUUIDString(id), "deployment_id": pgUUIDString(id), "kind": string(input.Kind)})
				err = db.EnqueueDurableNotificationTx(ctx, tx, db.NotifyEnvironmentWorkloadImage, string(payload))
			}
		}
		if err != nil {
			return nil, mapErr(err)
		}
		dep, err := q.EnvironmentGitOpsImageCandidate(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, EnvironmentWorkloadCandidate{DeploymentID: pgUUIDString(dep.ID), AppID: pgUUIDString(dep.AppID), Resource: frozen.Resource,
			Status: DeploymentStatus(dep.Status), HasRootfs: dep.RootfsPath != "" || dep.RootfsKey != ""})
	}
	// A long preparation must not commit after its issued lease expires.
	if err := lockEnvironmentGitOps(ctx, tx, lease, time.Now()); err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}
