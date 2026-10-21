package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func admitPromotionMigrationDB(ctx context.Context, tx pgx.Tx, p ProjectEnvironmentPromotion) error {
	c, err := readPromotionReleaseCaptureDB(ctx, tx, p.AccountID, p.ID)
	if err != nil {
		return err
	}
	if err = validatePromotionMigrationAdmission(p, c); err != nil {
		return err
	}
	if c == nil || c.Migration == nil {
		return nil
	}
	allowed, err := sqlc.New().PromotionMigrationAdmissionAllowed(ctx, tx, sqlc.PromotionMigrationAdmissionAllowedParams{ProjectID: p.ProjectID, PromotionID: p.ID})
	if err != nil {
		return mapErr(err)
	}
	if !allowed.Valid || !allowed.Bool {
		return ErrConflict
	}
	return mapErr(sqlc.New().InsertPromotionMigration(ctx, tx, p.ID))
}

func readPromotionMigrationDB(ctx context.Context, db sqlc.DBTX, id string) (promotionMigrationCapture, error) {
	row, err := sqlc.New().ReadPromotionMigration(ctx, db, id)
	if err != nil {
		return promotionMigrationCapture{}, mapErr(err)
	}
	return promotionMigrationCapture{row.TaskID, row.RuntimeFingerprint, row.PreparedLeaseToken}, nil
}

func promotionMigrationCandidateDB(ctx context.Context, db sqlc.DBTX, p ProjectEnvironmentPromotion, appID string) (Deployment, error) {
	raw, err := sqlc.New().ReadPromotionReleaseCandidate(ctx, db, sqlc.ReadPromotionReleaseCandidateParams{AccountID: p.AccountID, PromotionID: p.ID, AppID: appID})
	if err != nil {
		return Deployment{}, mapErr(err)
	}
	var stored struct {
		projectCloneArtifact
		Status                 DeploymentStatus `json:"status"`
		TrafficPercent         int              `json:"traffic_percent"`
		TrafficPercentExplicit bool             `json:"traffic_percent_explicit"`
	}
	if json.Unmarshal(raw, &stored) != nil {
		return Deployment{}, ErrConflict
	}
	d := stored.deployment("", stored.Scope, ProjectEnvironmentWorkloadSettings{})
	d.ID, d.Status, d.TrafficPercent, d.TrafficPercentExplicit = stored.ID, stored.Status, stored.TrafficPercent, stored.TrafficPercentExplicit
	return d, nil
}

func (s *PgStore) ReconcileProjectEnvironmentPromotionMigration(ctx context.Context, accountID, id string) (*api.ProjectPromotionHealth, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	projectID, err := q.PromotionHealthProject(ctx, tx, sqlc.PromotionHealthProjectParams{AccountID: accountID, PromotionID: id})
	if err != nil {
		return nil, mapErr(err)
	}
	if err = lockProjectForReleaseSetTx(ctx, tx, accountID, projectID); err != nil {
		return nil, err
	}
	if _, err = q.LockPromotionHealthOwner(ctx, tx, sqlc.LockPromotionHealthOwnerParams{AccountID: accountID, PromotionID: id}); err != nil {
		return nil, mapErr(err)
	}
	h, err := readPromotionHealthDB(ctx, tx, accountID, id)
	if err != nil || h == nil {
		return nil, err
	}
	if h.Phase != "migrating" || h.Policy.Migration == nil {
		return h, nil
	}
	p, err := readPromotionMigrationOwnerDB(ctx, tx, accountID, id)
	if err != nil {
		return nil, err
	}
	c, err := readPromotionReleaseCaptureDB(ctx, tx, accountID, id)
	if err != nil || c == nil || c.Migration == nil {
		return nil, ErrConflict
	}
	members := []ProjectReleaseMember{}
	for _, w := range c.Workloads {
		d, err := promotionMigrationCandidateDB(ctx, tx, p, w.AppID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return h, nil
			}
			return nil, err
		}
		members = append(members, ProjectReleaseMember{AppID: w.AppID, DeploymentID: d.ID})
	}
	if err = guardPromotionReleaseConfigurationDB(ctx, tx, p, members, false, false); err != nil {
		return nil, err
	}
	d, err := promotionMigrationCandidateDB(ctx, tx, p, c.Migration.AppID)
	if err != nil {
		return nil, err
	}
	pin, err := readPromotionMigrationDB(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if pin.TaskID == "" && time.Now().Before(h.StartedAt.Add(api.ProjectPromotionMigrationStartTimeout)) {
		snapshot, err := runtimeAppValuesDB(ctx, tx, p.AccountID, d.AppID, d.ID)
		if err != nil {
			return nil, err
		}
		fence, err := NewRuntimeAppConfigFence(snapshot)
		if err != nil {
			return nil, err
		}
		pin.TaskID, pin.RuntimeFingerprint = uuid.NewString(), fence.Fingerprint
		if _, err = q.CapturePromotionMigrationTask(ctx, tx, sqlc.CapturePromotionMigrationTaskParams{PromotionID: id, TaskID: pin.TaskID, Fingerprint: pin.RuntimeFingerprint}); err != nil {
			return nil, mapErr(err)
		}
		params, err := promotionMigrationTaskParams(p, h, d)
		if err != nil {
			return nil, err
		}
		if err = q.InsertPromotionMigrationTask(ctx, tx, sqlc.InsertPromotionMigrationTaskParams{TaskID: pin.TaskID, DeploymentID: d.ID, TimeoutSeconds: int32(params.TimeoutSeconds), OutputBytes: int32(params.MaxOutputBytes), StartDeadline: pgtype.Timestamptz{Time: *params.StartDeadlineAt, Valid: true}}); err != nil {
			return nil, mapErr(err)
		}
	}
	var task *AppTask
	if pin.TaskID != "" {
		t, err := readPromotionMigrationTaskDB(ctx, tx, d.ID)
		if err != nil {
			return nil, err
		}
		task = &t
	}
	now := time.Now().UTC()
	next := promotionMigrationOutcome(p.AccountID, h, d, task, now)

	if next.Phase == "preflight" && verifyPromotionMigrationTask(p.AccountID, h, d, *task, pin) != nil {
		return nil, ErrConflict
	}
	if next.Generation == h.Generation {
		return next, nil
	}
	if err = savePromotionHealthDB(ctx, tx, id, h.Generation, next); err != nil {
		return nil, err
	}
	status, verification, message := promotionHealthResult(next)
	if err = q.UpdatePromotionHealthResult(ctx, tx, sqlc.UpdatePromotionHealthResultParams{PromotionID: id, Status: status, VerificationStatus: verification, ErrorMessage: message}); err != nil {
		return nil, mapErr(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	return next, nil
}

func readPromotionMigrationOwnerDB(ctx context.Context, db sqlc.DBTX, accountID, id string) (ProjectEnvironmentPromotion, error) {
	r, err := sqlc.New().ReadPromotionMigrationOwner(ctx, db, sqlc.ReadPromotionMigrationOwnerParams{AccountID: accountID, PromotionID: id})
	if err != nil {
		return ProjectEnvironmentPromotion{}, mapErr(err)
	}
	return ProjectEnvironmentPromotion{ID: id, AccountID: accountID, ProjectID: r.ProjectID, FromEnvironment: r.FromEnvironment, ToEnvironment: r.ToEnvironment, SourceReleaseSetID: r.SourceReleaseID, PreviousTargetReleaseSetID: r.PreviousReleaseID, Status: r.Status, SyncConfig: true, ReleaseGraphMode: true}, nil
}

func readPromotionMigrationTaskDB(ctx context.Context, db sqlc.DBTX, deploymentID string) (AppTask, error) {
	row, err := sqlc.New().ReadProjectReleaseCommandTask(ctx, db, deploymentID)
	if err != nil {
		return AppTask{}, mapErr(err)
	}
	return AppTask{ID: row.ID, AccountID: row.AccountID, AppID: row.AppID, DeploymentID: row.DeploymentID, Kind: AppTaskKind(row.Kind), Command: row.Command, CommandShell: row.CommandShell, DeploymentScope: row.DeploymentScope, ArtifactKey: row.ArtifactKey, ImageDigest: row.ImageDigest, Status: AppTaskStatus(row.Status), ExitCode: executionIntPtr(row.ExitCode), FailureCode: executionStringPtr(row.FailureCode), StartedAt: timestamptzToTimePtr(row.StartedAt)}, nil
}

func guardPromotionMigrationRuntimeDB(ctx context.Context, db sqlc.DBTX, task AppTask, prepared bool) error {
	r, err := sqlc.New().ReadPromotionMigrationForTask(ctx, db, task.ID)
	if errors.Is(mapErr(err), ErrNotFound) {
		return nil
	}
	if err != nil {
		return mapErr(err)
	}
	if err = sqlc.New().LockSafeProjectReleaseProject(ctx, db, sqlc.LockSafeProjectReleaseProjectParams{AccountID: r.AccountID, ProjectID: r.ProjectID}); err != nil {
		return mapErr(err)
	}
	h, err := readPromotionHealthDB(ctx, db, r.AccountID, r.PromotionID)
	if err != nil {
		return err
	}
	if h == nil || h.Phase != "migrating" || !time.Now().Before(h.Deadline) || task.DeploymentScope != r.ToEnvironment || prepared && (task.LeaseToken == nil || r.PreparedLeaseToken != *task.LeaseToken) {
		return ErrConflict
	}
	tx, ok := db.(pgx.Tx)
	if !ok {
		return ErrConflict
	}
	p, err := readPromotionMigrationOwnerDB(ctx, tx, r.AccountID, r.PromotionID)
	if err != nil {
		return err
	}
	if p.Status != "running" {
		return ErrConflict
	}
	c, err := readPromotionReleaseCaptureDB(ctx, tx, p.AccountID, p.ID)
	if err != nil || c == nil {
		return ErrConflict
	}
	members := []ProjectReleaseMember{}
	for _, w := range c.Workloads {
		d, err := promotionMigrationCandidateDB(ctx, tx, p, w.AppID)
		if err != nil {
			return err
		}
		members = append(members, ProjectReleaseMember{AppID: w.AppID, DeploymentID: d.ID})
	}
	if err = guardPromotionReleaseConfigurationDB(ctx, tx, p, members, false, false); err != nil {
		return err
	}
	if _, err = sqlc.New().LockBindingPromotionRevision(ctx, db, sqlc.LockBindingPromotionRevisionParams{AccountID: mustPgUUID(task.AccountID), AppID: mustPgUUID(task.AppID)}); err != nil {
		return mapErr(err)
	}
	snapshot, err := runtimeAppValuesDB(ctx, db, task.AccountID, task.AppID, task.DeploymentID)
	if err != nil {
		return err
	}
	fence, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil || fence.Fingerprint != r.RuntimeFingerprint {
		return ErrConflict
	}
	return nil
}

func bindPromotionMigrationRuntimeDB(ctx context.Context, tx pgx.Tx, task, passed AppTask, fence RuntimeAppConfigFence) (bool, error) {
	r, err := sqlc.New().ReadPromotionMigrationForTask(ctx, tx, task.ID)
	if errors.Is(mapErr(err), ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return true, mapErr(err)
	}
	if task.Status != AppTaskRestoring || task.LeaseToken == nil || passed.LeaseToken == nil || *task.LeaseToken != *passed.LeaseToken || task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(time.Now()) || !validRuntimeAppConfigFence(fence) || fence.Fingerprint != r.RuntimeFingerprint {
		return true, ErrConflict
	}
	if err = guardPromotionMigrationRuntimeDB(ctx, tx, task, false); err != nil {
		return true, err
	}
	return true, mapErr(sqlc.New().AttestPromotionMigrationRuntime(ctx, tx, sqlc.AttestPromotionMigrationRuntimeParams{TaskID: task.ID, Token: *task.LeaseToken, Fingerprint: fence.Fingerprint}))
}

func guardPromotionMigrationPublishDB(ctx context.Context, tx pgx.Tx, p ProjectEnvironmentPromotion, h *api.ProjectPromotionHealth) error {
	if h.Policy.Migration == nil {
		return nil
	}
	pin, err := readPromotionMigrationDB(ctx, tx, p.ID)
	if err != nil {
		return err
	}
	d, err := promotionMigrationCandidateDB(ctx, tx, p, h.Policy.Migration.AppID)
	if err != nil {
		return err
	}
	task, err := readPromotionMigrationTaskDB(ctx, tx, d.ID)
	if err != nil {
		return err
	}
	if err = verifyPromotionMigrationTask(p.AccountID, h, d, task, pin); err != nil {
		r, readErr := readPromotionRecoveryDB(ctx, tx, p.AccountID, p.ID, "")
		if readErr != nil || r == nil {
			return ErrConflict
		}
		runtime, readErr := sqlc.New().ReadPromotionMigrationRuntime(ctx, tx, p.ID)
		if readErr != nil {
			return mapErr(readErr)
		}
		if !promotionRecoveredMigrationEligible(p.AccountID, h, d, task, pin, *r, promotionMigrationRuntime{NodeID: runtime.NodeID, InstanceID: runtime.InstanceID, LeaseToken: runtime.LeaseToken, DestroyedAt: timestamptzToTimePtr(runtime.DestroyedAt)}) {
			return ErrConflict
		}
	}
	snapshot, err := runtimeAppValuesDB(ctx, tx, p.AccountID, d.AppID, d.ID)
	if err != nil {
		return err
	}
	fence, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil || fence.Fingerprint != pin.RuntimeFingerprint {
		return ErrConflict
	}
	return nil
}
