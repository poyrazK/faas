package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type promotionRecoveryDBState struct {
	P       ProjectEnvironmentPromotion
	H       *api.ProjectPromotionHealth
	Pin     promotionMigrationCapture
	Runtime promotionMigrationRuntime
}

func readPromotionRecoveryDB(ctx context.Context, db sqlc.DBTX, account, id, request string) (*promotionRecoveryRecord, error) {
	q := sqlc.New()
	var raw []byte
	var err error
	if request == "" {
		raw, err = q.LatestPromotionRecovery(ctx, db, sqlc.LatestPromotionRecoveryParams{AccountID: account, PromotionID: id})
	} else {
		raw, err = q.ReadPromotionRecovery(ctx, db, sqlc.ReadPromotionRecoveryParams{AccountID: account, PromotionID: id, RequestID: request})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	var r promotionRecoveryRecord
	if json.Unmarshal(raw, &r) != nil || !r.Receipt.Request.Valid() || r.Receipt.OperatorAccountID != account {
		return nil, ErrConflict
	}
	return &r, nil
}
func savePromotionRecoveryDB(ctx context.Context, db sqlc.DBTX, id string, r promotionRecoveryRecord, previous string) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	n, err := sqlc.New().SavePromotionRecovery(ctx, db, sqlc.SavePromotionRecoveryParams{PromotionID: id, RequestID: r.Receipt.Request.RequestID, PreviousStatus: previous, Status: r.Receipt.Status, Payload: raw})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func promotionRecoveryOwnerDB(ctx context.Context, tx pgx.Tx, account, id string) (ProjectEnvironmentPromotion, error) {
	r, err := sqlc.New().LockPromotionHealthOwner(ctx, tx, sqlc.LockPromotionHealthOwnerParams{AccountID: account, PromotionID: id})
	if err != nil {
		return ProjectEnvironmentPromotion{}, mapErr(err)
	}
	return ProjectEnvironmentPromotion{ID: r.ID, AccountID: r.AccountID, ProjectID: r.ProjectID, ProjectSlug: r.ProjectSlug, FromEnvironment: r.FromEnvironment, ToEnvironment: r.ToEnvironment, Status: r.Status, SourceReleaseSetID: r.SourceReleaseID, TargetReleaseSetID: r.TargetReleaseID, PreviousTargetReleaseSetID: r.PreviousReleaseID, RestoredTargetReleaseSetID: r.RestoredReleaseID, SyncConfig: r.SyncConfig, ReleaseGraphMode: true, SourceConfigHash: r.SourceConfigHash, PreviousTargetConfigHash: r.PreviousTargetConfigHash, TargetConfigVersion: r.TargetConfigVersion, RollbackConfigVersion: r.RollbackConfigVersion, SourceConfigSnapshot: r.SourceConfigSnapshot, PreviousTargetConfigSnapshot: r.PreviousTargetConfigSnapshot}, nil
}
func lockPromotionRecoveryDB(ctx context.Context, tx pgx.Tx, account, id string) (promotionRecoveryDBState, error) {
	var c promotionRecoveryDBState
	q := sqlc.New()
	project, err := q.PromotionHealthProject(ctx, tx, sqlc.PromotionHealthProjectParams{AccountID: account, PromotionID: id})
	if err != nil {
		return c, mapErr(err)
	}
	if err = lockProjectForReleaseSetTx(ctx, tx, account, project); err != nil {
		return c, err
	}
	c.P, err = promotionRecoveryOwnerDB(ctx, tx, account, id)
	if err != nil {
		return c, err
	}
	c.H, err = readPromotionHealthDB(ctx, tx, account, id)
	if err != nil {
		return c, err
	}
	c.Pin, err = readPromotionMigrationDB(ctx, tx, id)
	if err != nil {
		return c, err
	}
	runtime, err := q.ReadPromotionMigrationRuntime(ctx, tx, id)
	if err != nil {
		return c, mapErr(err)
	}
	c.Runtime = promotionMigrationRuntime{NodeID: runtime.NodeID, InstanceID: runtime.InstanceID, LeaseToken: runtime.LeaseToken, DestroyedAt: timestamptzToTimePtr(runtime.DestroyedAt)}
	return c, nil
}
func recoveryTaskDB(ctx context.Context, db sqlc.DBTX, c promotionRecoveryDBState) (AppTask, error) {
	row, err := sqlc.New().ReadPromotionRecoveryTask(ctx, db, c.Pin.TaskID)
	if err != nil {
		return AppTask{}, mapErr(err)
	}
	return appTaskFromSQLC(row)
}
func recoveryResumeGuardDB(ctx context.Context, tx pgx.Tx, c promotionRecoveryDBState, r promotionRecoveryRecord) error {
	capture, err := readPromotionReleaseCaptureDB(ctx, tx, c.P.AccountID, c.P.ID)
	if err != nil || capture == nil {
		return ErrConflict
	}
	members := []ProjectReleaseMember{}
	for _, w := range capture.Workloads {
		d, err := promotionMigrationCandidateDB(ctx, tx, c.P, w.AppID)
		if err != nil {
			return err
		}
		members = append(members, ProjectReleaseMember{AppID: w.AppID, DeploymentID: d.ID})
	}
	if err = guardPromotionReleaseConfigurationDB(ctx, tx, c.P, members, false, false); err != nil {
		return err
	}
	d, err := promotionMigrationCandidateDB(ctx, tx, c.P, c.H.Policy.Migration.AppID)
	if err != nil {
		return err
	}
	snapshot, err := runtimeAppValuesDB(ctx, tx, c.P.AccountID, d.AppID, d.ID)
	if err != nil {
		return err
	}
	fence, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil || fence.Fingerprint != r.RuntimeFingerprint {
		return ErrConflict
	}
	return nil
}
func (s *PgStore) ProjectPromotionMigrationRecovery(ctx context.Context, account, id string) (*api.ProjectPromotionMigrationRecovery, error) {
	r, err := readPromotionRecoveryDB(ctx, s.pool, account, id, "")
	if err != nil || r == nil {
		return nil, err
	}
	return copyPromotionRecovery(*r), nil
}
func (s *PgStore) RequestProjectPromotionMigrationRecovery(ctx context.Context, account, id string, req api.ProjectPromotionMigrationRecoveryRequest) (*api.ProjectPromotionMigrationRecovery, error) {
	if !req.Valid() {
		return nil, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	c, err := lockPromotionRecoveryDB(ctx, tx, account, id)
	if err != nil {
		return nil, err
	}
	old, err := readPromotionRecoveryDB(ctx, tx, account, id, req.RequestID)
	if err != nil {
		return nil, err
	}
	if old != nil {
		if !promotionRecoveryMatches(*old, req) {
			return nil, ErrConflict
		}
		return copyPromotionRecovery(*old), nil
	}
	previous, err := readPromotionRecoveryDB(ctx, tx, account, id, "")
	if err != nil {
		return nil, err
	}
	if previous != nil && previous.Receipt.Status != "blocked" {
		return nil, ErrConflict
	}
	task, err := recoveryTaskDB(ctx, tx, c)
	if err != nil {
		return nil, err
	}
	if c.H == nil || c.H.Generation != req.ExpectedGeneration || c.Pin.TaskID != req.ExpectedTaskID || !promotionRecoveryTaskEligible(c.P, c.H, task, c.Pin, c.Runtime) {
		return nil, ErrConflict
	}
	allowed, laneErr := sqlc.New().PromotionRecoveryLaneAllowed(ctx, tx, sqlc.PromotionRecoveryLaneAllowedParams{ProjectID: c.P.ProjectID, PromotionID: id, TaskID: task.ID})
	if laneErr != nil {
		return nil, mapErr(laneErr)
	}
	if !allowed.Valid || !allowed.Bool {
		return nil, ErrConflict
	}
	r := newPromotionRecovery(account, req, c.Pin, time.Now().UTC())
	if req.Action == "resume" {
		if err = recoveryResumeGuardDB(ctx, tx, c, r); err != nil {
			return nil, err
		}
	} else {
		otherProject, projectErr := sqlc.New().PromotionHealthProject(ctx, tx, sqlc.PromotionHealthProjectParams{AccountID: account, PromotionID: req.ReplacementPromotionID})
		if projectErr != nil {
			return nil, mapErr(projectErr)
		}
		if otherProject != c.P.ProjectID {
			return nil, ErrConflict
		}
		other, err := promotionRecoveryOwnerDB(ctx, tx, account, req.ReplacementPromotionID)
		if err != nil {
			return nil, err
		}
		h, err := readPromotionHealthDB(ctx, tx, account, other.ID)
		if err != nil {
			return nil, err
		}
		active, err := activeProjectReleaseSetIDTx(ctx, tx, c.P.ProjectID, c.P.ToEnvironment)
		if err != nil {
			return nil, err
		}
		if !promotionRecoveryReplacementEligible(c.P, other, h) || active != other.TargetReleaseSetID {
			return nil, ErrConflict
		}
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	if err = sqlc.New().InsertPromotionRecovery(ctx, tx, sqlc.InsertPromotionRecoveryParams{PromotionID: id, RequestID: req.RequestID, RequestHash: req.Fingerprint(), Payload: raw}); err != nil {
		return nil, mapErr(err)
	}
	next := clonePromotionHealth(c.H)
	next.Generation++
	if err = savePromotionHealthDB(ctx, tx, id, c.H.Generation, next); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	return copyPromotionRecovery(r), nil
}
func (s *PgStore) FinalizeProjectPromotionMigrationRecovery(ctx context.Context, account, id, request string, e ProjectMigrationRecoveryEvidence) (*api.ProjectPromotionMigrationRecovery, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	c, err := lockPromotionRecoveryDB(ctx, tx, account, id)
	if err != nil {
		return nil, err
	}
	r, err := readPromotionRecoveryDB(ctx, tx, account, id, request)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, ErrNotFound
	}
	if promotionRecoveryTerminal(r.Receipt.Status) {
		return copyPromotionRecovery(*r), nil
	}
	task, err := recoveryTaskDB(ctx, tx, c)
	if err != nil {
		return nil, err
	}
	if r.Receipt.Status != "verifying" || !time.Now().Before(r.Receipt.Deadline) || c.Runtime.DestroyedAt == nil || !promotionRecoveryTaskEligible(c.P, c.H, task, c.Pin, c.Runtime) {
		return nil, ErrConflict
	}
	if r.Receipt.Request.Action == "resume" {
		if err = recoveryResumeGuardDB(ctx, tx, c, *r); err != nil {
			return nil, err
		}
		r.Receipt.Status = "resumed"
	} else {
		if err = verifyPromotionRecoveryReplacementDB(ctx, tx, c.P, r, e); err != nil {
			return nil, err
		}
		r.Receipt.Status = "resolved"
		r.RuntimeFingerprints = e.RuntimeFingerprints
	}
	now := time.Now().UTC()
	r.Receipt.ResolvedAt = &now
	if err = savePromotionRecoveryDB(ctx, tx, id, *r, "verifying"); err != nil {
		return nil, err
	}
	if r.Receipt.Status == "resumed" {
		next := promotionRecoveryResumeHealth(c.H, now)
		if err = savePromotionHealthDB(ctx, tx, id, c.H.Generation, next); err != nil {
			return nil, err
		}
		status, verification, message := promotionHealthResult(next)
		if err = sqlc.New().UpdatePromotionHealthResult(ctx, tx, sqlc.UpdatePromotionHealthResultParams{PromotionID: id, Status: status, VerificationStatus: verification, ErrorMessage: message}); err != nil {
			return nil, mapErr(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	return copyPromotionRecovery(*r), nil
}
func verifyPromotionRecoveryReplacementDB(ctx context.Context, tx pgx.Tx, p ProjectEnvironmentPromotion, r *promotionRecoveryRecord, e ProjectMigrationRecoveryEvidence) error {
	other, err := promotionRecoveryOwnerDB(ctx, tx, p.AccountID, r.Receipt.Request.ReplacementPromotionID)
	if err != nil {
		return err
	}
	h, err := readPromotionHealthDB(ctx, tx, p.AccountID, other.ID)
	if err != nil {
		return err
	}
	active, err := activeProjectReleaseSetIDTx(ctx, tx, p.ProjectID, p.ToEnvironment)
	if err != nil {
		return err
	}
	if !promotionRecoveryReplacementEligible(p, other, h) || active != other.TargetReleaseSetID || !promotionRecoveryEvidenceEligible(h, e, time.Now()) {
		return ErrConflict
	}
	graph, err := readProjectReleaseSetTx(ctx, tx, p.AccountID, p.ProjectID, p.ToEnvironment, active)
	if err != nil {
		return err
	}
	if len(e.RuntimeFingerprints) != len(graph.Members) {
		return ErrConflict
	}
	if err = lockPromotionWorkloadEnvironmentsTx(ctx, tx, other.ID); err != nil {
		return err
	}
	if guardPromotionReleaseConfigurationDB(ctx, tx, other, graph.Members, false, true) != nil || verifyProjectEnvironmentPromotionConfigTx(ctx, tx, other, false) != nil {
		return ErrConflict
	}
	for _, member := range graph.Members {
		snapshot, err := runtimeAppValuesDB(ctx, tx, p.AccountID, member.AppID, member.DeploymentID)
		if err != nil {
			return err
		}
		fence, err := NewRuntimeAppConfigFence(snapshot)
		if err != nil || fence.Fingerprint != e.RuntimeFingerprints[member.AppID] {
			return ErrConflict
		}
	}
	if len(api.ProjectPromotionWorkers(&h.Policy)) > 0 {
		q := sqlc.New()
		for _, worker := range api.ProjectPromotionWorkers(&h.Policy) {
			deployment, err := q.PromotionWorkerDeployment(ctx, tx, sqlc.PromotionWorkerDeploymentParams{PromotionID: other.ID, AppID: worker.AppID})
			if err != nil {
				return mapErr(err)
			}
			if _, err = q.LockPromotionRecoveryWorkerConsumer(ctx, tx, worker.BindingID); err != nil {
				return mapErr(err)
			}
			if _, err = q.LockPromotionRecoveryWorkerInstances(ctx, tx, deployment); err != nil {
				return mapErr(err)
			}
		}

		now := time.Now().UTC()
		observed := promotionRecoveryWorkerObservation(h)
		if err = observePromotionWorkerDB(ctx, tx, other, observed, now); err != nil {
			return err
		}
		var proofs []*promotionRecoveryWorkerProof
		for _, one := range promotionWorkerHealthViews(observed) {
			sample := promotionRecoveryWorkerSample(one, r.Receipt.RequestedAt)
			if err = readWorkerHealthDB(ctx, tx, sample, now); err != nil {
				return err
			}
			proof, err := promotionRecoveryWorkerProofFor(one, sample, r.Receipt.RequestedAt, now)
			if err != nil {
				return err
			}
			proofs = append(proofs, proof)
		}
		if h.Policy.Worker != nil {
			r.WorkerProof = proofs[0]
		} else {
			r.WorkerProofs = proofs
		}
	}
	return nil
}
func (s *PgStore) BlockProjectPromotionMigrationRecovery(ctx context.Context, account, id, request, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = lockPromotionRecoveryDB(ctx, tx, account, id); err != nil {
		return err
	}
	r, err := readPromotionRecoveryDB(ctx, tx, account, id, request)
	if err != nil {
		return err
	}
	if r == nil {
		return ErrNotFound
	}
	if promotionRecoveryTerminal(r.Receipt.Status) {
		return nil
	}
	previous := r.Receipt.Status
	r.Receipt.Status, r.Receipt.Reason = "blocked", reason
	if err = savePromotionRecoveryDB(ctx, tx, id, *r, previous); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}
func (s *PgStore) RegisterPromotionMigrationRuntime(ctx context.Context, passed AppTask, node string) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	_, err = q.ReadPromotionMigrationForTask(ctx, tx, passed.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", mapErr(err)
	}
	row, err := q.ReadMigrationTaskForUpdate(ctx, tx, passed.ID)
	if err != nil {
		return "", mapErr(err)
	}
	task, err := appTaskFromSQLC(row)
	if err != nil {
		return "", err
	}
	if task.Status != AppTaskRestoring || task.LeaseToken == nil || passed.LeaseToken == nil || *task.LeaseToken != *passed.LeaseToken || task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(time.Now()) {
		return "", ErrConflict
	}
	if err = guardPromotionMigrationRuntimeDB(ctx, tx, task, true); err != nil {
		return "", err
	}
	instance := promotionMigrationInstanceID(task.ID, *task.LeaseToken)
	n, err := q.RegisterPromotionMigrationRuntime(ctx, tx, sqlc.RegisterPromotionMigrationRuntimeParams{NodeID: node, InstanceID: instance, Token: *task.LeaseToken, TaskID: task.ID})
	if err != nil {
		return "", mapErr(err)
	}
	if n != 1 {
		return "", ErrConflict
	}
	if err = tx.Commit(ctx); err != nil {
		return "", mapErr(err)
	}
	return instance, nil
}
func (s *PgStore) PendingPromotionMigrationQuiescence(ctx context.Context) ([]PromotionMigrationQuiescenceJob, error) {
	rows, err := sqlc.New().PendingPromotionRecoveryQuiescence(ctx, s.pool, api.ProjectPromotionHealthBatchSize)
	if err != nil {
		return nil, mapErr(err)
	}
	jobs := []PromotionMigrationQuiescenceJob{}
	for _, r := range rows {
		jobs = append(jobs, PromotionMigrationQuiescenceJob{AccountID: r.AccountID, PromotionID: r.PromotionID, RequestID: r.RequestID, TaskID: r.TaskID, NodeID: r.NodeID, InstanceID: r.InstanceID, LeaseToken: r.LeaseToken})
	}
	return jobs, nil
}
func (s *PgStore) AcknowledgePromotionMigrationQuiescence(ctx context.Context, job PromotionMigrationQuiescenceJob) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	c, err := lockPromotionRecoveryDB(ctx, tx, job.AccountID, job.PromotionID)
	if err != nil {
		return err
	}
	r, err := readPromotionRecoveryDB(ctx, tx, job.AccountID, job.PromotionID, job.RequestID)
	if err != nil {
		return err
	}
	if r == nil {
		return ErrNotFound
	}
	if r.Receipt.Status != "quiescing" {
		return nil
	}
	task, err := recoveryTaskDB(ctx, tx, c)
	if err != nil {
		return err
	}
	if c.Pin.TaskID != job.TaskID || c.Runtime.NodeID != job.NodeID || c.Runtime.InstanceID != job.InstanceID || c.Runtime.LeaseToken != job.LeaseToken || !promotionRecoveryTaskEligible(c.P, c.H, task, c.Pin, c.Runtime) {
		return ErrConflict
	}
	if err = sqlc.New().MarkPromotionMigrationDestroyed(ctx, tx, job.PromotionID); err != nil {
		return mapErr(err)
	}
	r.Receipt.Status = "verifying"
	if err = savePromotionRecoveryDB(ctx, tx, job.PromotionID, *r, "quiescing"); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}

func guardPromotionMigrationDispatchDB(ctx context.Context, db sqlc.DBTX, task AppTask) error {
	owner, err := sqlc.New().ReadPromotionMigrationForTask(ctx, db, task.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return mapErr(err)
	}
	runtime, err := sqlc.New().ReadPromotionMigrationRuntime(ctx, db, owner.PromotionID)
	if err != nil {
		return mapErr(err)
	}
	if task.LeaseToken == nil || runtime.LeaseToken != *task.LeaseToken || runtime.NodeID == "" || runtime.InstanceID != promotionMigrationInstanceID(task.ID, *task.LeaseToken) {
		return ErrConflict
	}
	return nil
}
