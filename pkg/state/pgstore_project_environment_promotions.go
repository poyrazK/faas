package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func scanProjectEnvironmentPromotion(row pgx.Row) (ProjectEnvironmentPromotion, error) {
	var promotion ProjectEnvironmentPromotion
	if err := row.Scan(
		&promotion.ID, &promotion.AccountID, &promotion.ProjectID, &promotion.ProjectSlug,
		&promotion.FromEnvironment, &promotion.ToEnvironment, &promotion.PromotionHash,
		&promotion.IdempotencyKey, &promotion.Status, &promotion.Error,
		&promotion.CreatedAt, &promotion.UpdatedAt, &promotion.CompletedAt,
		&promotion.RollbackStatus, &promotion.RollbackIdempotencyKey, &promotion.RollbackError,
		&promotion.RollbackStartedAt, &promotion.RollbackCompletedAt,
		&promotion.VerificationStatus, &promotion.VerificationError,
		&promotion.VerificationStartedAt, &promotion.VerificationCompletedAt,
	); err != nil {
		return ProjectEnvironmentPromotion{}, mapErr(err)
	}
	return promotion, nil
}

func scanProjectEnvironmentPromotionWorkload(row pgx.Row) (ProjectEnvironmentPromotionWorkload, error) {
	var workload ProjectEnvironmentPromotionWorkload
	if err := row.Scan(
		&workload.ID, &workload.PromotionID, &workload.WorkloadSlug, &workload.WorkloadName,
		&workload.SourceDeploymentID, &workload.PreviousTargetDeploymentID,
		&workload.TargetDeploymentID, &workload.Status, &workload.Error,
		&workload.RollbackStatus, &workload.RestoredTargetDeploymentID, &workload.RollbackError,
		&workload.VerificationStatus, &workload.VerificationError,
		&workload.CreatedAt, &workload.UpdatedAt,
	); err != nil {
		return ProjectEnvironmentPromotionWorkload{}, mapErr(err)
	}
	return workload, nil
}

func (s *PgStore) CreateProjectEnvironmentPromotion(ctx context.Context, promotion ProjectEnvironmentPromotion, workloads []ProjectEnvironmentPromotionWorkload) (ProjectEnvironmentPromotion, []ProjectEnvironmentPromotionWorkload, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ProjectEnvironmentPromotion{}, nil, fmt.Errorf("state: begin environment promotion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx, `
		insert into project_environment_promotions
			(account_id, project_id, project_slug, from_environment, to_environment,
			 promotion_hash, idempotency_key, status, error, verification_status)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		returning id, account_id, project_id, project_slug, from_environment,
		          to_environment, promotion_hash, idempotency_key, status, error,
		          created_at, updated_at, completed_at, rollback_status,
		          rollback_idempotency_key, rollback_error, rollback_started_at,
		          rollback_completed_at, verification_status, verification_error,
		          verification_started_at, verification_completed_at
	`, promotion.AccountID, promotion.ProjectID, promotion.ProjectSlug,
		promotion.FromEnvironment, promotion.ToEnvironment, promotion.PromotionHash,
		promotion.IdempotencyKey, promotion.Status, promotion.Error, promotion.VerificationStatus)
	created, err := scanProjectEnvironmentPromotion(row)
	if err != nil {
		return ProjectEnvironmentPromotion{}, nil, err
	}

	createdWorkloads := make([]ProjectEnvironmentPromotionWorkload, 0, len(workloads))
	for _, workload := range workloads {
		row := tx.QueryRow(ctx, `
			insert into project_environment_promotion_workloads
				(promotion_id, workload_slug, workload_name, source_deployment_id,
				 previous_target_deployment_id, target_deployment_id, status, error)
			values ($1, $2, $3, $4, $5, $6, $7, $8)
			returning id, promotion_id, workload_slug, workload_name, source_deployment_id,
			          previous_target_deployment_id, target_deployment_id, status, error,
				          rollback_status, restored_target_deployment_id, rollback_error,
				          verification_status, verification_error,
				          created_at, updated_at
		`, created.ID, workload.WorkloadSlug, workload.WorkloadName,
			workload.SourceDeploymentID, workload.PreviousTargetDeploymentID,
			workload.TargetDeploymentID, workload.Status, workload.Error)
		createdWorkload, err := scanProjectEnvironmentPromotionWorkload(row)
		if err != nil {
			return ProjectEnvironmentPromotion{}, nil, err
		}
		createdWorkloads = append(createdWorkloads, createdWorkload)
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironmentPromotion{}, nil, fmt.Errorf("state: commit environment promotion: %w", err)
	}
	return created, createdWorkloads, nil
}

func (s *PgStore) ProjectEnvironmentPromotionByID(ctx context.Context, accountID, projectSlug, targetEnvironment, id string) (ProjectEnvironmentPromotion, []ProjectEnvironmentPromotionWorkload, error) {
	promotion, err := scanProjectEnvironmentPromotion(s.pool.QueryRow(ctx, `
		select id, account_id, project_id, project_slug, from_environment, to_environment,
		       promotion_hash, idempotency_key, status, error, created_at, updated_at, completed_at,
		       rollback_status, rollback_idempotency_key, rollback_error, rollback_started_at,
		       rollback_completed_at, verification_status, verification_error,
		       verification_started_at, verification_completed_at
		  from project_environment_promotions
		 where id = $1 and account_id = $2 and project_slug = $3 and to_environment = $4
	`, id, accountID, projectSlug, targetEnvironment))
	if err != nil {
		return ProjectEnvironmentPromotion{}, nil, err
	}
	workloads, err := s.listProjectEnvironmentPromotionWorkloads(ctx, promotion.ID)
	if err != nil {
		return ProjectEnvironmentPromotion{}, nil, err
	}
	return promotion, workloads, nil
}

func (s *PgStore) ProjectEnvironmentPromotionByIdempotencyKey(ctx context.Context, accountID, projectSlug, idempotencyKey string) (ProjectEnvironmentPromotion, []ProjectEnvironmentPromotionWorkload, error) {
	promotion, err := scanProjectEnvironmentPromotion(s.pool.QueryRow(ctx, `
		select id, account_id, project_id, project_slug, from_environment, to_environment,
		       promotion_hash, idempotency_key, status, error, created_at, updated_at, completed_at,
		       rollback_status, rollback_idempotency_key, rollback_error, rollback_started_at,
		       rollback_completed_at, verification_status, verification_error,
		       verification_started_at, verification_completed_at
		  from project_environment_promotions
		 where account_id = $1 and project_slug = $2 and idempotency_key = $3
	`, accountID, projectSlug, idempotencyKey))
	if err != nil {
		return ProjectEnvironmentPromotion{}, nil, err
	}
	workloads, err := s.listProjectEnvironmentPromotionWorkloads(ctx, promotion.ID)
	if err != nil {
		return ProjectEnvironmentPromotion{}, nil, err
	}
	return promotion, workloads, nil
}

// ListProjectEnvironmentPromotionsBefore returns newest-first promotion
// history for one target environment. The compound (created_at, id) cursor
// keeps equal-timestamp rows stable across pages and uses the existing
// account/project/environment lookup index.
func (s *PgStore) ListProjectEnvironmentPromotionsBefore(ctx context.Context, accountID, projectSlug, targetEnvironment, sourceEnvironment, status string, before time.Time, beforeID string, limit int) ([]ProjectEnvironmentPromotion, error) {
	conditions := []string{
		"account_id = $1",
		"project_slug = $2",
		"to_environment = $3",
	}
	args := []any{accountID, projectSlug, targetEnvironment}
	if sourceEnvironment != "" {
		args = append(args, sourceEnvironment)
		conditions = append(conditions, fmt.Sprintf("from_environment = $%d", len(args)))
	}
	if status != "" {
		args = append(args, status)
		conditions = append(conditions, fmt.Sprintf("status = $%d", len(args)))
	}
	if !before.IsZero() {
		args = append(args, before)
		beforePos := len(args)
		if beforeID != "" {
			args = append(args, beforeID)
			conditions = append(conditions, fmt.Sprintf("(created_at, id) < ($%d, $%d::uuid)", beforePos, len(args)))
		} else {
			conditions = append(conditions, fmt.Sprintf("created_at < $%d", beforePos))
		}
	}
	query := `select id, account_id, project_id, project_slug, from_environment, to_environment,
	                 promotion_hash, idempotency_key, status, error, created_at, updated_at, completed_at,
	                 rollback_status, rollback_idempotency_key, rollback_error, rollback_started_at,
	                 rollback_completed_at, verification_status, verification_error,
	                 verification_started_at, verification_completed_at
	            from project_environment_promotions
	           where ` + strings.Join(conditions, " and ") + `
	           order by created_at desc, id desc`
	if limit > 0 {
		args = append(args, limit)
		query += fmt.Sprintf(" limit $%d", len(args))
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("state: list environment promotions: %w", err)
	}
	defer rows.Close()
	items := make([]ProjectEnvironmentPromotion, 0)
	for rows.Next() {
		promotion, err := scanProjectEnvironmentPromotion(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, promotion)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list environment promotions rows: %w", err)
	}
	return items, nil
}

func (s *PgStore) listProjectEnvironmentPromotionWorkloads(ctx context.Context, promotionID string) ([]ProjectEnvironmentPromotionWorkload, error) {
	rows, err := s.pool.Query(ctx, `
		select id, promotion_id, workload_slug, workload_name, source_deployment_id,
		       previous_target_deployment_id, target_deployment_id, status, error,
		       rollback_status, restored_target_deployment_id, rollback_error,
		       verification_status, verification_error,
		       created_at, updated_at
		  from project_environment_promotion_workloads
		 where promotion_id = $1
		 order by created_at, id
	`, promotionID)
	if err != nil {
		return nil, fmt.Errorf("state: list environment promotion workloads: %w", err)
	}
	defer rows.Close()
	workloads := make([]ProjectEnvironmentPromotionWorkload, 0)
	for rows.Next() {
		workload, err := scanProjectEnvironmentPromotionWorkload(rows)
		if err != nil {
			return nil, err
		}
		workloads = append(workloads, workload)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list environment promotion workloads rows: %w", err)
	}
	return workloads, nil
}

func (s *PgStore) UpdateProjectEnvironmentPromotion(ctx context.Context, accountID, id, status, errorMessage string, completedAt *time.Time) (ProjectEnvironmentPromotion, error) {
	return scanProjectEnvironmentPromotion(s.pool.QueryRow(ctx, `
		update project_environment_promotions
		   set status = $3, error = $4, updated_at = now(), completed_at = $5
		 where id = $1 and account_id = $2
		returning id, account_id, project_id, project_slug, from_environment, to_environment,
		          promotion_hash, idempotency_key, status, error, created_at, updated_at, completed_at,
		          rollback_status, rollback_idempotency_key, rollback_error, rollback_started_at,
		          rollback_completed_at, verification_status, verification_error,
		          verification_started_at, verification_completed_at
	`, id, accountID, status, errorMessage, completedAt))
}

func (s *PgStore) StartProjectEnvironmentPromotionRollback(ctx context.Context, accountID, id, idempotencyKey string) (ProjectEnvironmentPromotion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentPromotion{}, fmt.Errorf("state: begin environment promotion rollback: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := scanProjectEnvironmentPromotion(tx.QueryRow(ctx, `
		select id, account_id, project_id, project_slug, from_environment, to_environment,
		       promotion_hash, idempotency_key, status, error, created_at, updated_at, completed_at,
		       rollback_status, rollback_idempotency_key, rollback_error, rollback_started_at,
		       rollback_completed_at, verification_status, verification_error,
		       verification_started_at, verification_completed_at
		  from project_environment_promotions
		 where id = $1 and account_id = $2
		 for update
	`, id, accountID))
	if err != nil {
		return ProjectEnvironmentPromotion{}, err
	}
	if current.RollbackIdempotencyKey != "" && current.RollbackIdempotencyKey != idempotencyKey {
		return ProjectEnvironmentPromotion{}, ErrConflict
	}
	if current.RollbackStatus == "rolled_back" {
		if err := tx.Commit(ctx); err != nil {
			return ProjectEnvironmentPromotion{}, fmt.Errorf("state: commit environment promotion rollback replay: %w", err)
		}
		return current, nil
	}
	returning := tx.QueryRow(ctx, `
		update project_environment_promotions
		   set rollback_status = 'rolling_back',
		       rollback_idempotency_key = $3,
		       rollback_error = '',
		       rollback_started_at = coalesce(rollback_started_at, now()),
		       rollback_completed_at = null,
		       updated_at = now()
		 where id = $1 and account_id = $2
		returning id, account_id, project_id, project_slug, from_environment, to_environment,
		          promotion_hash, idempotency_key, status, error, created_at, updated_at, completed_at,
		          rollback_status, rollback_idempotency_key, rollback_error, rollback_started_at,
		          rollback_completed_at, verification_status, verification_error,
		          verification_started_at, verification_completed_at
	`, id, accountID, idempotencyKey)
	updated, err := scanProjectEnvironmentPromotion(returning)
	if err != nil {
		return ProjectEnvironmentPromotion{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironmentPromotion{}, fmt.Errorf("state: commit environment promotion rollback: %w", err)
	}
	return updated, nil
}

func (s *PgStore) UpdateProjectEnvironmentPromotionRollback(ctx context.Context, accountID, id, status, errorMessage string, completedAt *time.Time) (ProjectEnvironmentPromotion, error) {
	return scanProjectEnvironmentPromotion(s.pool.QueryRow(ctx, `
		update project_environment_promotions
		   set rollback_status = $3, rollback_error = $4, updated_at = now(), rollback_completed_at = $5
		 where id = $1 and account_id = $2
		returning id, account_id, project_id, project_slug, from_environment, to_environment,
		          promotion_hash, idempotency_key, status, error, created_at, updated_at, completed_at,
		          rollback_status, rollback_idempotency_key, rollback_error, rollback_started_at,
		          rollback_completed_at, verification_status, verification_error,
		          verification_started_at, verification_completed_at
	`, id, accountID, status, errorMessage, completedAt))
}

func (s *PgStore) UpdateProjectEnvironmentPromotionWorkload(ctx context.Context, accountID, promotionID, workloadID, status, targetDeploymentID, errorMessage string) (ProjectEnvironmentPromotionWorkload, error) {
	return scanProjectEnvironmentPromotionWorkload(s.pool.QueryRow(ctx, `
		update project_environment_promotion_workloads w
		   set status = $4, target_deployment_id = $5, error = $6, updated_at = now()
		 where w.id = $1 and w.promotion_id = $2
		   and exists (
				select 1 from project_environment_promotions p
				 where p.id = w.promotion_id and p.account_id = $3
		   )
		returning w.id, w.promotion_id, w.workload_slug, w.workload_name,
		          w.source_deployment_id, w.previous_target_deployment_id,
		          w.target_deployment_id, w.status, w.error,
		          w.rollback_status, w.restored_target_deployment_id, w.rollback_error,
		          w.verification_status, w.verification_error,
		          w.created_at, w.updated_at
	`, workloadID, promotionID, accountID, status, targetDeploymentID, errorMessage))
}

func (s *PgStore) UpdateProjectEnvironmentPromotionRollbackWorkload(ctx context.Context, accountID, promotionID, workloadID, status, restoredTargetDeploymentID, errorMessage string) (ProjectEnvironmentPromotionWorkload, error) {
	return scanProjectEnvironmentPromotionWorkload(s.pool.QueryRow(ctx, `
		update project_environment_promotion_workloads w
		   set rollback_status = $4, restored_target_deployment_id = $5,
		       rollback_error = $6, updated_at = now()
		 where w.id = $1 and w.promotion_id = $2
		   and exists (
				select 1 from project_environment_promotions p
				 where p.id = w.promotion_id and p.account_id = $3
		   )
		returning w.id, w.promotion_id, w.workload_slug, w.workload_name,
		          w.source_deployment_id, w.previous_target_deployment_id,
		          w.target_deployment_id, w.status, w.error,
		          w.rollback_status, w.restored_target_deployment_id, w.rollback_error,
		          w.verification_status, w.verification_error,
		          w.created_at, w.updated_at
	`, workloadID, promotionID, accountID, status, restoredTargetDeploymentID, errorMessage))
}

func (s *PgStore) UpdateProjectEnvironmentPromotionVerification(ctx context.Context, accountID, id, status, errorMessage string, startedAt, completedAt *time.Time) (ProjectEnvironmentPromotion, error) {
	return scanProjectEnvironmentPromotion(s.pool.QueryRow(ctx, `
		update project_environment_promotions
		   set verification_status = $3, verification_error = $4,
		       verification_started_at = coalesce($5, verification_started_at),
		       verification_completed_at = $6, updated_at = now()
		 where id = $1 and account_id = $2
		returning id, account_id, project_id, project_slug, from_environment, to_environment,
		          promotion_hash, idempotency_key, status, error, created_at, updated_at, completed_at,
		          rollback_status, rollback_idempotency_key, rollback_error, rollback_started_at,
		          rollback_completed_at, verification_status, verification_error,
		          verification_started_at, verification_completed_at
	`, id, accountID, status, errorMessage, startedAt, completedAt))
}

func (s *PgStore) UpdateProjectEnvironmentPromotionVerificationWorkload(ctx context.Context, accountID, promotionID, workloadID, status, errorMessage string) (ProjectEnvironmentPromotionWorkload, error) {
	return scanProjectEnvironmentPromotionWorkload(s.pool.QueryRow(ctx, `
		update project_environment_promotion_workloads w
		   set verification_status = $4, verification_error = $5, updated_at = now()
		 where w.id = $1 and w.promotion_id = $2
		   and exists (
				select 1 from project_environment_promotions p
				 where p.id = w.promotion_id and p.account_id = $3
		   )
		returning w.id, w.promotion_id, w.workload_slug, w.workload_name,
		          w.source_deployment_id, w.previous_target_deployment_id,
		          w.target_deployment_id, w.status, w.error,
		          w.rollback_status, w.restored_target_deployment_id, w.rollback_error,
		          w.verification_status, w.verification_error,
		          w.created_at, w.updated_at
	`, workloadID, promotionID, accountID, status, errorMessage))
}
