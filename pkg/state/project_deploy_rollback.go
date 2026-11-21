package state

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

type ProjectDeployRollbackStore interface {
	LatestProjectRollbackBatch(context.Context, string, string) (*api.ProjectRollbackBatch, error)
}

func projectRollbackGroupEligible(group, scope string) bool {
	if group == "" {
		return false
	}
	id, err := uuid.Parse(group)
	return err == nil && id != uuid.Nil && routeRemovalProductionScope(scope)
}

func recordProjectDeployGroup(ctx context.Context, tx pgx.Tx, d Deployment, group string) error {
	if !projectRollbackGroupEligible(group, d.Scope) {
		return ErrInvalidArgument
	}
	tag, err := tx.Exec(ctx, `
		insert into project_deploy_rollback_members (deployment_id, project_id, deploy_group_id)
		select $1, project_id, $2 from apps where id = $3 and project_id is not null`,
		d.ID, group, d.AppID)
	if err != nil {
		return fmt.Errorf("state: record project deployment membership: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrInvalidArgument
	}
	return nil
}

func (s *PgStore) LatestProjectRollbackBatch(ctx context.Context, accountID, projectID string) (*api.ProjectRollbackBatch, error) {
	rows, err := s.pool.Query(ctx, `
		with latest as (
			select m.deploy_group_id
			  from project_deploy_rollback_members m
			  join projects p on p.id = m.project_id and p.account_id = $1
			  join deployments d on d.id = m.deployment_id
			  join apps a on a.id = d.app_id and a.project_id = p.id and a.status <> 'deleted'
			 where m.project_id = $2
			   and d.deleted_at is null and d.status = 'live' and d.traffic_percent = 100 and d.rollout_state = 'complete'
			   and (d.canary_total_steps = 0 or d.canary_step >= d.canary_total_steps)
			 order by d.created_at desc, d.id desc
			 limit 1
		)
		select m.deploy_group_id::text, coalesce(a.slug, ''), d.id::text,
		       d.revision, d.scope, d.status, d.serving_ended_at is not null,
		       coalesce(predecessor.id::text, ''), coalesce(predecessor.revision, 0)
		  from project_deploy_rollback_members m
		  join latest on latest.deploy_group_id = m.deploy_group_id
		  join deployments d on d.id = m.deployment_id
		  left join apps a on a.id = d.app_id and a.project_id = m.project_id and a.status <> 'deleted'
		  left join deployment_recovery_lineage lineage on lineage.deployment_id = d.id
		  left join deployments predecessor on predecessor.id = lineage.predecessor_deployment_id
		 where m.project_id = $2
		 order by a.slug, d.id`, accountID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var batch api.ProjectRollbackBatch
	for rows.Next() {
		var member api.ProjectRollbackMember
		if err := rows.Scan(&batch.ID, &member.Slug, &member.DeploymentID, &member.Revision,
			&member.Scope, &member.Status, &member.PreviouslyServed, &member.PredecessorID, &member.PredecessorRevision); err != nil {
			return nil, err
		}
		batch.Members = append(batch.Members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if batch.ID == "" {
		return nil, nil
	}
	return &batch, nil
}

func (m *MemStore) LatestProjectRollbackBatch(_ context.Context, accountID, projectID string) (*api.ProjectRollbackBatch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	project, found := m.projects[projectID]
	if !found || project.AccountID != accountID {
		return nil, nil
	}
	var latest Deployment
	for _, d := range m.deployments {
		app := m.apps[d.AppID]
		if d.projectDeployProjectID != projectID || app.ProjectID != projectID || app.Status == AppDeleted ||
			d.Status != DeployLive || d.TrafficPercent != 100 || d.RolloutState != "complete" ||
			(d.CanaryTotalSteps > 0 && d.CanaryStep < d.CanaryTotalSteps) {
			continue
		}
		if latest.ID == "" || d.CreatedAt.After(latest.CreatedAt) || (d.CreatedAt.Equal(latest.CreatedAt) && d.ID > latest.ID) {
			latest = d
		}
	}
	if latest.ID == "" {
		return nil, nil
	}
	batch := &api.ProjectRollbackBatch{ID: latest.ProjectDeployGroupID}
	for _, d := range m.deployments {
		if d.projectDeployProjectID != projectID || d.ProjectDeployGroupID != batch.ID {
			continue
		}
		app := m.apps[d.AppID]
		var slug string
		if app.ProjectID == projectID && app.Status != AppDeleted {
			slug = app.Slug
		}
		predecessor := m.deployments[m.recoveryPredecessors[d.ID]]
		batch.Members = append(batch.Members, api.ProjectRollbackMember{
			Slug: slug, DeploymentID: d.ID, Revision: d.Revision, Scope: normalizedDeploymentScope(d.Scope),
			Status: string(d.Status), PreviouslyServed: m.deploymentServedLocked(d.ID),
			PredecessorID: predecessor.ID, PredecessorRevision: predecessor.Revision,
		})
	}
	return batch, nil
}
