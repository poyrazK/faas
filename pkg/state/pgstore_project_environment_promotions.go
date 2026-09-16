package state

import (
	"context"
	"fmt"
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
			 promotion_hash, idempotency_key, status, error)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		returning id, account_id, project_id, project_slug, from_environment,
		          to_environment, promotion_hash, idempotency_key, status, error,
		          created_at, updated_at, completed_at
	`, promotion.AccountID, promotion.ProjectID, promotion.ProjectSlug,
		promotion.FromEnvironment, promotion.ToEnvironment, promotion.PromotionHash,
		promotion.IdempotencyKey, promotion.Status, promotion.Error)
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
		       promotion_hash, idempotency_key, status, error, created_at, updated_at, completed_at
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
		       promotion_hash, idempotency_key, status, error, created_at, updated_at, completed_at
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

func (s *PgStore) listProjectEnvironmentPromotionWorkloads(ctx context.Context, promotionID string) ([]ProjectEnvironmentPromotionWorkload, error) {
	rows, err := s.pool.Query(ctx, `
		select id, promotion_id, workload_slug, workload_name, source_deployment_id,
		       previous_target_deployment_id, target_deployment_id, status, error,
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
		          promotion_hash, idempotency_key, status, error, created_at, updated_at, completed_at
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
		          w.target_deployment_id, w.status, w.error, w.created_at, w.updated_at
	`, workloadID, promotionID, accountID, status, targetDeploymentID, errorMessage))
}
