package state

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

type ProjectDeployRollbackStore interface {
	LatestProjectRollbackBatch(context.Context, string, string) (*api.ProjectRollbackBatch, error)
}

type ProjectRollbackTargetsStore interface {
	ProjectRollbackBatchByID(context.Context, string, string, string) (*api.ProjectRollbackBatch, error)
	ListProjectRollbackTargets(context.Context, string, string) ([]api.ProjectRollbackTargetDeploy, error)
}

const projectRollbackRestorableSQL = `d.deleted_at is null and (
	(d.serving_ended_at is not null and (d.status = 'superseded' or (d.status = 'live' and d.traffic_percent = 0)))
	or (d.status = 'live' and d.traffic_percent = 100 and d.rollout_state = 'complete'
	    and (d.canary_total_steps = 0 or d.canary_step >= d.canary_total_steps))
)`

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
	return s.projectRollbackBatch(ctx, accountID, projectID, "")
}

func (s *PgStore) ProjectRollbackBatchByID(ctx context.Context, accountID, projectID, group string) (*api.ProjectRollbackBatch, error) {
	if group == "" {
		return nil, ErrInvalidArgument
	}
	return s.projectRollbackBatch(ctx, accountID, projectID, group)
}

func (s *PgStore) projectRollbackBatch(ctx context.Context, accountID, projectID, group string) (*api.ProjectRollbackBatch, error) {
	rows, err := s.pool.Query(ctx, `
		with selected as (
			select nullif($3, '')::uuid as deploy_group_id where $3 <> ''
			union all (
			select m.deploy_group_id
			  from project_deploy_rollback_members m
			  join projects p on p.id = m.project_id and p.account_id = $1
			  join deployments d on d.id = m.deployment_id
			  join apps a on a.id = d.app_id and a.project_id = p.id and a.status <> 'deleted'
			 where m.project_id = $2 and $3 = ''
			   and d.deleted_at is null and d.status = 'live' and d.traffic_percent = 100 and d.rollout_state = 'complete'
			   and (d.canary_total_steps = 0 or d.canary_step >= d.canary_total_steps)
			 order by d.created_at desc, d.id desc
			 limit 1)
		)
		select m.deploy_group_id::text, coalesce(a.slug, ''), d.id::text,
		       d.revision, d.scope, d.status, d.serving_ended_at is not null,
		       coalesce(predecessor.id::text, ''), coalesce(predecessor.revision, 0),
		       (`+projectRollbackRestorableSQL+`)
		  from project_deploy_rollback_members m
		  join projects p on p.id = m.project_id and p.account_id = $1
		  join selected on selected.deploy_group_id = m.deploy_group_id
		  join deployments d on d.id = m.deployment_id
		  left join apps a on a.id = d.app_id and a.project_id = m.project_id and a.status <> 'deleted'
		  left join deployment_recovery_lineage lineage on lineage.deployment_id = d.id
		  left join deployments predecessor on predecessor.id = lineage.predecessor_deployment_id
		 where m.project_id = $2
		 order by a.slug, d.id`, accountID, projectID, group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var batch api.ProjectRollbackBatch
	for rows.Next() {
		var member api.ProjectRollbackMember
		if err := rows.Scan(&batch.ID, &member.Slug, &member.DeploymentID, &member.Revision,
			&member.Scope, &member.Status, &member.PreviouslyServed, &member.PredecessorID, &member.PredecessorRevision, &member.Restorable); err != nil {
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

func (s *PgStore) ListProjectRollbackTargets(ctx context.Context, accountID, projectID string) ([]api.ProjectRollbackTargetDeploy, error) {
	rows, err := s.pool.Query(ctx, `
		with targets as (
		    select m.deploy_group_id, max(d.created_at) as created_at, count(*) as service_count,
		           array_agg(d.id::text order by d.id) as deployments
		      from project_deploy_rollback_members m
		      join projects p on p.id = m.project_id and p.account_id = $1
		      join deployments d on d.id = m.deployment_id
		      left join apps a on a.id = d.app_id and a.project_id = p.id and a.status <> 'deleted'
		     where m.project_id = $2
		     group by m.deploy_group_id
		    having count(distinct a.id) = count(*) and bool_and(`+projectRollbackRestorableSQL+`)
		     order by created_at desc, m.deploy_group_id desc
		     limit $3
		)
		select t.deploy_group_id::text, t.created_at, t.service_count, h.expires_at, coalesce(h.layer_bytes,0)
		  from targets t
		  left join lateral (
		      select expires_at, layer_bytes from project_rollback_deploy_protections
		       where project_id = $2 and deploy_group_id = t.deploy_group_id
		         and expires_at > clock_timestamp() and deployment_ids = t.deployments
		         and storage_keys = array(
		             select key from (
		                 select rootfs_key::text as key from deployments where id = any(t.deployments::uuid[])
		                 union
		                 select storage_key::text from deployment_sidecar_layers where deployment_id = any(t.deployments::uuid[])
		             ) k order by key collate "C"
		         )
		       order by expires_at desc limit 1
		  ) h on true
		 order by t.created_at desc, t.deploy_group_id desc`, accountID, projectID, api.ProjectRollbackTargetsLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]api.ProjectRollbackTargetDeploy, 0)
	for rows.Next() {
		var deploy api.ProjectRollbackTargetDeploy
		var expires *time.Time
		var layerBytes int64
		if err := rows.Scan(&deploy.DeployGroupID, &deploy.CreatedAt, &deploy.ServiceCount, &expires, &layerBytes); err != nil {
			return nil, err
		}
		if expires != nil {
			deploy.Protection = &api.ProjectRollbackProtection{DeployGroupID: deploy.DeployGroupID, ExpiresAt: *expires, LayerBytes: layerBytes}
		}
		out = append(out, deploy)
	}
	return out, rows.Err()
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
	return m.projectRollbackBatchLocked(projectID, latest.ProjectDeployGroupID), nil
}

func (m *MemStore) ProjectRollbackBatchByID(_ context.Context, accountID, projectID, group string) (*api.ProjectRollbackBatch, error) {
	if group == "" {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if project, ok := m.projects[projectID]; !ok || project.AccountID != accountID {
		return nil, nil
	}
	return m.projectRollbackBatchLocked(projectID, group), nil
}

func (m *MemStore) projectRollbackBatchLocked(projectID, group string) *api.ProjectRollbackBatch {
	batch := &api.ProjectRollbackBatch{ID: group}
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
			PredecessorID: predecessor.ID, PredecessorRevision: predecessor.Revision, Restorable: m.projectRollbackRestorableLocked(d),
		})
	}
	if len(batch.Members) == 0 {
		return nil
	}
	return batch
}

func (m *MemStore) projectRollbackRestorableLocked(d Deployment) bool {
	return d.DeletedAt == nil && (m.deploymentServedLocked(d.ID) &&
		(d.Status == DeploySuperseded || d.Status == DeployLive && d.TrafficPercent == 0) ||
		d.Status == DeployLive && d.TrafficPercent == 100 && d.RolloutState == "complete" &&
			(d.CanaryTotalSteps == 0 || d.CanaryStep >= d.CanaryTotalSteps))
}

func (m *MemStore) ListProjectRollbackTargets(_ context.Context, accountID, projectID string) ([]api.ProjectRollbackTargetDeploy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]api.ProjectRollbackTargetDeploy, 0)
	if project, ok := m.projects[projectID]; !ok || project.AccountID != accountID {
		return out, nil
	}
	type group struct {
		target  api.ProjectRollbackTargetDeploy
		apps    map[string]bool
		invalid bool
	}
	groups := make(map[string]*group)
	for _, d := range m.deployments {
		if d.projectDeployProjectID != projectID {
			continue
		}
		g := groups[d.ProjectDeployGroupID]
		if g == nil {
			g = &group{target: api.ProjectRollbackTargetDeploy{DeployGroupID: d.ProjectDeployGroupID}, apps: make(map[string]bool)}
			groups[d.ProjectDeployGroupID] = g
		}
		app := m.apps[d.AppID]
		g.invalid = g.invalid || app.ProjectID != projectID || app.Status == AppDeleted || g.apps[d.AppID] || !m.projectRollbackRestorableLocked(d)
		g.apps[d.AppID] = true
		if d.CreatedAt.After(g.target.CreatedAt) {
			g.target.CreatedAt = d.CreatedAt
		}
	}
	for _, g := range groups {
		if !g.invalid {
			g.target.ServiceCount = len(g.apps)
			out = append(out, g.target)
		}
	}
	slices.SortFunc(out, func(a, b api.ProjectRollbackTargetDeploy) int {
		return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), cmp.Compare(b.DeployGroupID, a.DeployGroupID))
	})
	out = out[:min(len(out), api.ProjectRollbackTargetsLimit)]
	if len(m.projectRollbackProtections) == 0 {
		return out, nil
	}
	now := time.Now()
	for i := range out {
		group := out[i].DeployGroupID
		artifacts, err := m.projectRollbackProtectionArtifactsLocked(accountID, projectID, group)
		if err != nil {
			continue
		}
		manifest, _, _, err := projectRollbackProtectionAssets(artifacts)
		if err != nil {
			continue
		}
		hold := m.projectRollbackProtections[projectRollbackProtectionKey{projectID, group, manifest.fingerprint()}]
		if hold.ExpiresAt.After(now) {
			out[i].Protection = &hold.ProjectRollbackProtection
		}
	}
	return out, nil
}
