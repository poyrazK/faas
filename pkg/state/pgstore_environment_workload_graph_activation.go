package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsGraphActivationStore = (*PgStore)(nil)

func environmentGitOpsManagedForScopeTx(ctx context.Context, tx pgx.Tx, appID, scope string) (bool, error) {
	var managed bool
	err := tx.QueryRow(ctx, `select exists (
		select 1 from deployments where app_id=$1 and scope=$2 and environment_workload_runtime is not null
	)`, appID, normalizedDeploymentScope(scope)).Scan(&managed)
	return managed, err
}

func environmentGitOpsManagedForAppTx(ctx context.Context, tx pgx.Tx, appID string) (bool, error) {
	var managed bool
	err := tx.QueryRow(ctx, `select exists (
		select 1 from deployments where app_id=$1 and environment_workload_runtime is not null
	)`, appID).Scan(&managed)
	return managed, err
}

func (s *PgStore) ActivateEnvironmentGitOpsWorkloadGraph(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) (ProjectReleaseSet, bool, error) {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return ProjectReleaseSet{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Release-set writers lock project -> active release -> apps -> deployments.
	// Take the project lock before candidate input and deployment locks too, or
	// a concurrent ordinary publish can form a project/deployment deadlock.
	var found int
	if err := tx.QueryRow(ctx, `select 1 from projects where id=$1::uuid and account_id=$2::uuid for update`,
		lease.Source.ProjectID, lease.Source.AccountID).Scan(&found); err != nil {
		return ProjectReleaseSet{}, false, mapErr(err)
	}
	inputs, _, _, err := s.environmentCandidateInputsTx(ctx, tx, lease, reviewed)
	if err != nil {
		return ProjectReleaseSet{}, false, err
	}
	row, err := sqlc.New().EnvironmentWorkloadGraphForPreparation(ctx, tx, sqlc.EnvironmentWorkloadGraphForPreparationParams{
		SourceID: mustPgUUID(lease.Source.ID), Generation: lease.Source.Generation, PlanHash: reviewed.Hash,
	})
	if err != nil {
		return ProjectReleaseSet{}, false, mapErr(err)
	}
	graph := workloadGraphFromSQL(row)
	if graph.Phase != "prepared" {
		return ProjectReleaseSet{}, false, nil
	}
	evidence, err := environmentWorkloadActivationEvidenceTx(ctx, tx, row)
	if err != nil {
		return ProjectReleaseSet{}, false, err
	}
	if evidence.Activated {
		release, err := activeProjectReleaseSetTx(ctx, tx, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
		if err != nil {
			return ProjectReleaseSet{}, false, err
		}
		return release, true, nil
	}
	if err := s.verifyEnvironmentGraphCandidatesTx(ctx, tx, lease, reviewed, graph, inputs); err != nil {
		return ProjectReleaseSet{}, false, err
	}
	if !evidence.Qualified || !environmentGraphSupportsProductionServing(graph) {
		return ProjectReleaseSet{}, false, nil
	}
	jobsReady, err := environmentGitOpsScheduledJobsReadyTx(ctx, tx, graph, "paused", true)
	if err != nil {
		return ProjectReleaseSet{}, false, err
	}
	if !jobsReady {
		return ProjectReleaseSet{}, false, nil
	}
	activeID, err := activeProjectReleaseSetIDTx(ctx, tx, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
	if err != nil {
		return ProjectReleaseSet{}, false, err
	}
	members, fallback, ttl, err := environmentGitOpsReleaseMembersTx(ctx, tx, lease.Source, graph, activeID)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return ProjectReleaseSet{}, false, nil
		}
		return ProjectReleaseSet{}, false, err
	}
	expectedActiveID := &activeID
	candidateFallbackExclusions := make(map[string]struct{}, len(graph.Members))
	for _, member := range graph.Members {
		if member.CandidateDeploymentID != "" {
			candidateFallbackExclusions[member.AppID] = struct{}{}
		}
	}
	release, err := publishProjectReleaseSetTx(ctx, tx, lease.Source.AccountID, lease.Source.ProjectID,
		lease.Source.EnvironmentSlug, expectedActiveID, fallback, ttl, members, true,
		func(ctx context.Context, tx pgx.Tx) error {
			if _, err := sqlc.New().SetEnvironmentGitOpsLeaseContext(ctx, tx, lease.LeaseToken); err != nil {
				return mapErr(err)
			}
			var activationToken string
			if err := tx.QueryRow(ctx, `select set_config('gregale.gitops_activation',$1,true)`, lease.LeaseToken).Scan(&activationToken); err != nil {
				return mapErr(err)
			}
			for _, member := range graph.Members {
				if member.CandidateDeploymentID == "" {
					continue
				}
				deployment, err := scanDeploymentWithRootfs(tx.QueryRow(ctx, `select `+deploymentSelectColumnsWithRootfs+
					` from deployments where id=$1::uuid for update`, member.CandidateDeploymentID))
				if err != nil {
					return err
				}
				if deployment.AppID != member.AppID || deployment.Status != DeploySnapshotting || !deployment.EnvironmentWorkloadHeld() ||
					deployment.EnvironmentWorkloadRuntime == "" || normalizedDeploymentScope(deployment.Scope) != normalizedDeploymentScope(lease.Source.EnvironmentSlug) {
					return ErrConflict
				}
				if err := s.checkDeploymentAutomations(ctx, tx, deployment); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `update deployments set status='live', environment_workload_held=false,
					traffic_percent=0, traffic_percent_explicit=true, error='', rollout_state='complete',
					rollout_completed_at=coalesce(rollout_completed_at,now()) where id=$1::uuid`, member.CandidateDeploymentID); err != nil {
					return mapErr(err)
				}
				snapshot, policySnapshot, err := s.captureDeploymentOpenAPISnapshotTx(ctx, tx, deployment)
				if err != nil {
					return err
				}
				if err := persistDeploymentSnapshotsDBTX(ctx, tx, snapshot, policySnapshot, true); err != nil {
					return err
				}
			}
			return activateEnvironmentGitOpsScheduledJobsTx(ctx, tx, graph)
		}, candidateFallbackExclusions)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return ProjectReleaseSet{}, false, nil
		}
		return ProjectReleaseSet{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectReleaseSet{}, false, mapErr(err)
	}
	return release, true, nil
}

func (s *PgStore) verifyEnvironmentGraphCandidatesTx(ctx context.Context, tx pgx.Tx, lease EnvironmentGitOpsLease,
	reviewed environmentsync.Plan, graph EnvironmentWorkloadGraph, inputs []Deployment) error {
	candidateIDs := map[string]string{}
	for _, member := range graph.Members {
		if member.CandidateDeploymentID == "" {
			continue
		}
		if _, duplicate := candidateIDs[member.Resource]; duplicate {
			return ErrConflict
		}
		candidateIDs[member.Resource] = member.CandidateDeploymentID
	}
	q := sqlc.New()
	for _, input := range inputs {
		frozen := candidateFrozenInputs(input)
		id, err := q.EnvironmentGitOpsCandidateByInput(ctx, tx, sqlc.EnvironmentGitOpsCandidateByInputParams{
			SourceID: frozen.SourceID, Generation: fmt.Sprint(frozen.Generation), Resource: frozen.Resource, PlanHash: frozen.PlanHash,
		})
		if err != nil {
			return mapErr(err)
		}
		if candidateIDs[frozen.Resource] != pgUUIDString(id) {
			return ErrConflict
		}
		var raw []byte
		var status string
		var held bool
		if err := tx.QueryRow(ctx, `select environment_workload_runtime,status,environment_workload_held from deployments where id=$1::uuid for update`, id).Scan(&raw, &status, &held); err != nil {
			return mapErr(err)
		}
		if status != string(DeploySnapshotting) || !held || !frozenCandidateInputsMatch(raw, frozen) {
			return ErrConflict
		}
	}
	if len(candidateIDs) != len(inputs) {
		return ErrConflict
	}
	if graph.SourceID != lease.Source.ID || graph.RevisionID != lease.Revision.ID || graph.Generation != lease.Source.Generation || graph.PlanHash != reviewed.Hash {
		return ErrConflict
	}
	return nil
}

func activeProjectReleaseSetTx(ctx context.Context, tx pgx.Tx, projectID, environment string) (ProjectReleaseSet, error) {
	var release ProjectReleaseSet
	var expires pgtype.Timestamptz
	err := tx.QueryRow(ctx, `select id::text,account_id::text,project_id::text,environment_slug,active,ttl_seconds,expires_at,created_at
		from project_release_sets where project_id=$1::uuid and environment_slug=$2 and active`,
		projectID, normalizedDeploymentScope(environment)).Scan(&release.ID, &release.AccountID, &release.ProjectID,
		&release.EnvironmentSlug, &release.Active, &release.TTLSeconds, &expires, &release.CreatedAt)
	if err != nil {
		return ProjectReleaseSet{}, mapErr(err)
	}
	if expires.Valid {
		t := expires.Time
		release.ExpiresAt = &t
	}
	rows, err := tx.Query(ctx, `select app_id::text,deployment_id::text from project_release_members where release_id=$1::uuid order by app_id`, release.ID)
	if err != nil {
		return ProjectReleaseSet{}, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var member ProjectReleaseMember
		if err := rows.Scan(&member.AppID, &member.DeploymentID); err != nil {
			return ProjectReleaseSet{}, mapErr(err)
		}
		release.Members = append(release.Members, member)
	}
	return release, mapErr(rows.Err())
}

func environmentGitOpsReleaseMembersTx(ctx context.Context, tx pgx.Tx, source EnvironmentGitSource, graph EnvironmentWorkloadGraph,
	activeID string) ([]ProjectReleaseMember, []ProjectReleaseMember, int, error) {
	activeTargets := map[string]string{}
	if activeID != "" {
		rows, err := tx.Query(ctx, `select app_id::text,deployment_id::text from project_release_members where release_id=$1::uuid order by app_id`, activeID)
		if err != nil {
			return nil, nil, 0, mapErr(err)
		}
		for rows.Next() {
			var member ProjectReleaseMember
			if err := rows.Scan(&member.AppID, &member.DeploymentID); err != nil {
				rows.Close()
				return nil, nil, 0, mapErr(err)
			}
			if _, duplicate := activeTargets[member.AppID]; duplicate {
				rows.Close()
				return nil, nil, 0, ErrConflict
			}
			activeTargets[member.AppID] = member.DeploymentID
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, nil, 0, mapErr(err)
		}
		rows.Close()
	}
	candidateTargets := map[string]string{}
	for _, member := range graph.Members {
		if member.CandidateDeploymentID == "" {
			continue
		}
		if graph.ResourceIDs[member.Resource] != member.AppID || candidateTargets[member.AppID] != "" {
			return nil, nil, 0, ErrConflict
		}
		candidateTargets[member.AppID] = member.CandidateDeploymentID
	}
	rows, err := tx.Query(ctx, `select id::text,coalesce(manifest,'{}'::jsonb) from apps
		where project_id=$1::uuid and account_id=$2::uuid and status<>'deleted' and coalesce(preview_of_slug,'')=''
		order by id for update`, source.ProjectID, source.AccountID)
	if err != nil {
		return nil, nil, 0, mapErr(err)
	}
	type appRow struct {
		id       string
		manifest []byte
	}
	apps := []appRow{}
	for rows.Next() {
		var app appRow
		if err := rows.Scan(&app.id, &app.manifest); err != nil {
			rows.Close()
			return nil, nil, 0, mapErr(err)
		}
		apps = append(apps, app)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, 0, mapErr(err)
	}
	rows.Close()
	if len(apps) == 0 || len(apps) > api.ProjectReleaseSetMaxMembers {
		return nil, nil, 0, ErrConflict
	}
	members := make([]ProjectReleaseMember, 0, len(apps))
	fallback := make([]ProjectReleaseMember, 0, len(apps))
	minimumTTL := int(^uint(0) >> 1)
	for _, app := range apps {
		var manifest AppManifest
		if json.Unmarshal(app.manifest, &manifest) != nil || manifest.RevisionPinTTLSeconds <= 0 {
			return nil, nil, 0, ErrConflict
		}
		minimumTTL = min(minimumTTL, manifest.RevisionPinTTLSeconds)
		target := candidateTargets[app.id]
		if target == "" {
			target = activeTargets[app.id]
		}
		if target == "" && activeID == "" {
			deploymentRows, err := tx.Query(ctx, `select d.id::text,d.traffic_percent,d.traffic_percent_explicit,
				exists(select 1 from deployment_revision_pins p where p.deployment_id=d.id and p.expires_at>now()) or
				exists(select 1 from project_release_members rm join project_release_sets rs on rs.id=rm.release_id
				 where rm.deployment_id=d.id and (rs.active or rs.expires_at>now())) or
				exists(select 1 from customer_operation_retained_deployment_refs r where r.deployment_id=d.id) as retained
				from deployments d where d.app_id=$1::uuid and d.scope=$2 and d.status='live' and not d.environment_workload_held
				order by d.revision desc,d.id for update of d`, app.id, normalizedDeploymentScope(source.EnvironmentSlug))
			if err != nil {
				return nil, nil, 0, mapErr(err)
			}
			type deploymentRow struct {
				id       string
				traffic  int
				explicit bool
				retained bool
			}
			eligible := []deploymentRow{}
			weighted := []deploymentRow{}
			for deploymentRows.Next() {
				var deployment deploymentRow
				if err := deploymentRows.Scan(&deployment.id, &deployment.traffic, &deployment.explicit, &deployment.retained); err != nil {
					deploymentRows.Close()
					return nil, nil, 0, mapErr(err)
				}
				eligible = append(eligible, deployment)
				if deployment.traffic > 0 {
					weighted = append(weighted, deployment)
				}
			}
			if err := deploymentRows.Err(); err != nil {
				deploymentRows.Close()
				return nil, nil, 0, mapErr(err)
			}
			deploymentRows.Close()
			if len(weighted) > 1 || len(weighted) == 1 && weighted[0].traffic != 100 {
				return nil, nil, 0, ErrConflict
			}
			if len(weighted) == 1 {
				target = weighted[0].id
				fallback = append(fallback, ProjectReleaseMember{AppID: app.id, DeploymentID: target})
			} else {
				for _, deployment := range eligible {
					if deployment.explicit || deployment.retained {
						target = deployment.id
						break
					}
				}
			}
		}
		if target == "" {
			return nil, nil, 0, ErrConflict
		}
		members = append(members, ProjectReleaseMember{AppID: app.id, DeploymentID: target})
	}
	if minimumTTL <= 0 || minimumTTL > api.RevisionPinMaxTTLSeconds {
		return nil, nil, 0, ErrConflict
	}
	if activeID == "" {
		appIDs := make([]string, 0, len(apps))
		for _, app := range apps {
			if candidateTargets[app.id] == "" {
				appIDs = append(appIDs, app.id)
			}
		}
		if err := validateProjectReleaseFallbackTx(ctx, tx, appIDs, source.EnvironmentSlug, fallback); err != nil {
			return nil, nil, 0, err
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].AppID < members[j].AppID })
	return members, fallback, minimumTTL, nil
}
