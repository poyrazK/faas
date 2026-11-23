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

type ProjectRollbackRecord struct {
	api.ProjectRollbackOperation
	ReleaseReviews      map[string][]ReleaseRun                     `json:"release_reviews,omitempty"`
	HealthProbes        map[string][]api.ProjectReleaseProbe        `json:"health_probes,omitempty"`
	LiveTrafficPolicies map[string]ProjectRollbackLiveTrafficPolicy `json:"live_traffic_policies,omitempty"`
	HTTPWorkloads       []string                                    `json:"http_workloads,omitempty"`
	Version             int64                                       `json:"-"`
	nextRun             time.Time
	startRequest        api.ProjectRollbackRequest
}

// The claim version fences both parent updates and admission of child rollbacks.
type ProjectRollbackOperationStore interface {
	CreateProjectRollback(context.Context, string, ProjectRollbackRecord) error
	GetProjectRollback(context.Context, string, string, string) (ProjectRollbackRecord, error)
	ClaimProjectRollback(context.Context, time.Duration) (ProjectRollbackRecord, error)
	SaveProjectRollback(context.Context, ProjectRollbackRecord, time.Duration) error
	ResumeProjectRollback(context.Context, string, ProjectRollbackRecord) error
}

func (s *PgStore) CreateProjectRollback(ctx context.Context, account string, r ProjectRollbackRecord) error {
	return createProjectRollback(ctx, s.pool, account, r)
}

func createProjectRollback(ctx context.Context, db sqlc.DBTX, account string, r ProjectRollbackRecord) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tag, err := db.Exec(ctx, `
		INSERT INTO project_rollback_operations (id, project_id, status, receipt, start_request_id, start_request_options)
		SELECT $1, id, $4, $5, NULLIF($6, '')::uuid,
		       CASE WHEN $6 = '' THEN NULL ELSE $7::jsonb END
		FROM projects WHERE id = $2 AND account_id = $3`,
		r.ID, r.ProjectID, account, r.Status, data, r.startRequest.RequestID, r.startRequest)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanProjectRollback(row pgx.Row) (ProjectRollbackRecord, error) {
	var r ProjectRollbackRecord
	var raw []byte
	if err := row.Scan(&r.Version, &r.nextRun, &raw); err != nil {
		return r, mapErr(err)
	}
	err := json.Unmarshal(raw, &r)
	return r, err
}

func (s *PgStore) GetProjectRollback(ctx context.Context, account, project, id string) (ProjectRollbackRecord, error) {
	return scanProjectRollback(s.pool.QueryRow(ctx, `
		SELECT o.version, o.next_run_at, o.receipt FROM project_rollback_operations o
		JOIN projects p ON p.id = o.project_id
		WHERE o.id = $1 AND p.id = $2 AND p.account_id = $3`, id, project, account))
}

func (s *PgStore) ClaimProjectRollback(ctx context.Context, lease time.Duration) (ProjectRollbackRecord, error) {
	return scanProjectRollback(s.pool.QueryRow(ctx, `
		WITH next AS (
			SELECT id FROM project_rollback_operations
			WHERE status = 'running' AND next_run_at <= clock_timestamp()
			ORDER BY next_run_at, id FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE project_rollback_operations o
		SET version = o.version + 1, next_run_at = clock_timestamp() + $1::double precision * interval '1 second'
		FROM next WHERE o.id = next.id
		RETURNING o.version, o.next_run_at, o.receipt`, lease.Seconds()))
}

func (s *PgStore) SaveProjectRollback(ctx context.Context, r ProjectRollbackRecord, delay time.Duration) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE project_rollback_operations
		SET status = $3, receipt = $4, version = version + 1,
		    next_run_at = clock_timestamp() + $5::double precision * interval '1 second'
		WHERE id = $1 AND version = $2 AND status = 'running' AND next_run_at > clock_timestamp() AND project_id = $6`,
		r.ID, r.Version, r.Status, data, delay.Seconds(), r.ProjectID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) ResumeProjectRollback(ctx context.Context, account string, r ProjectRollbackRecord) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `
		UPDATE project_rollback_operations o
		SET status = 'running', receipt = $4, version = version + 1, next_run_at = clock_timestamp()
		FROM projects p
		WHERE o.id = $1 AND o.version = $2 AND p.account_id = $3
		  AND p.id = o.project_id AND p.id = $5 AND o.status = 'blocked' AND NOT (o.receipt ? 'undo_operation_id')`, r.ID, r.Version, account, data, r.ProjectID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	if err := checkPgProjectRollbackResumeReview(ctx, tx, r.ProjectID); err != nil {
		return err
	}
	if err := continueProjectRollbackUndoArtifactHoldsTx(ctx, tx, r.ID, r.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type projectRollbackAdmission struct {
	operation string
	version   int64
	step      api.ProjectRollbackStep
}

type projectRollbackAdmissionKey struct{}

func WithProjectRollbackStep(ctx context.Context, r ProjectRollbackRecord, step api.ProjectRollbackStep) context.Context {
	return context.WithValue(ctx, projectRollbackAdmissionKey{}, projectRollbackAdmission{operation: r.ID, version: r.Version, step: step})
}

func projectRollbackAdmissionFrom(ctx context.Context) (projectRollbackAdmission, bool) {
	admission, ok := ctx.Value(projectRollbackAdmissionKey{}).(projectRollbackAdmission)
	return admission, ok
}

func (a projectRollbackAdmission) validate(r ProjectRollbackRecord, app App, target, current Deployment) error {
	if r.Version != a.version || r.Status != "running" || app.ProjectID != r.ProjectID ||
		app.ID != a.step.AppID || target.ID != a.step.TargetID || current.ID != a.step.CurrentID ||
		!routeRemovalProductionScope(target.Scope) || !sameServingLane(target.Scope, a.step.Scope) ||
		!sameProjectServingPeriod(current.RolloutCompletedAt, a.step.CurrentServedAt) {
		return ErrCheckedRollbackChanged
	}
	if r.Phase != "" && (r.Phase != api.ProjectRollbackPreparing || r.PreparationDeadline == nil ||
		!time.Now().Before(*r.PreparationDeadline)) {
		return ErrCheckedRollbackChanged
	}
	for _, step := range r.Steps {
		switch step.Status {
		case "skipped", "rolled_back":
			continue
		case "planned":
			if step.AppID == app.ID && step.AttemptID != "" && step.AttemptID == a.step.AttemptID &&
				step.CurrentID == current.ID && step.TargetID == target.ID &&
				sameProjectServingPeriod(step.CurrentServedAt, a.step.CurrentServedAt) {
				return nil
			}
		}
		if r.Phase == "" {
			break
		}
	}
	return ErrCheckedRollbackChanged
}

func sameProjectServingPeriod(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func lockProjectRollbackClaim(ctx context.Context, tx pgx.Tx, id string) (ProjectRollbackRecord, error) {
	r, err := scanProjectRollback(tx.QueryRow(ctx, `
		SELECT version, next_run_at, receipt FROM project_rollback_operations
		WHERE id = $1 AND status = 'running' AND next_run_at > clock_timestamp() FOR UPDATE`, id))
	if errors.Is(err, ErrNotFound) {
		return r, ErrCheckedRollbackChanged
	}
	return r, err
}
