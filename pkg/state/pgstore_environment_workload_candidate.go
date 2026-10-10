package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsPreparationStore = (*PgStore)(nil)

func (s *PgStore) environmentCandidateInputsTx(ctx context.Context, tx pgx.Tx, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]Deployment, gitOpsIntentSnapshot, EnvironmentDesiredRevision, error) {
	q := sqlc.New()
	if _, err := q.LockEnvironmentGitOpsCandidateApps(ctx, tx, mustPgUUID(lease.Source.ID)); err != nil {
		return nil, gitOpsIntentSnapshot{}, EnvironmentDesiredRevision{}, mapErr(err)
	}
	source, revision, desired, err := lockApprovedEnvironmentGitOps(ctx, tx, lease.Source.AccountID, lease.Source.ID)
	if err != nil {
		return nil, gitOpsIntentSnapshot{}, revision, err
	}
	observed, snapshot, err := readEnvironmentGitOpsIntent(ctx, tx, source, desired)
	if err != nil {
		return nil, gitOpsIntentSnapshot{}, revision, err
	}
	plan, err := environmentGitOpsPlan(source, revision, desired, observed, false)
	if err != nil {
		return nil, gitOpsIntentSnapshot{}, revision, err
	}
	if plan.Hash != reviewed.Hash {
		return nil, gitOpsIntentSnapshot{}, revision, fmt.Errorf("%w: candidate plan no longer matches the current intent", ErrConflict)
	}
	rows, err := tx.Query(ctx, `SELECT steps FROM environment_gitops_runs WHERE source_id=$1::uuid AND revision_id=$2::uuid AND generation=$3`,
		mustPgUUID(source.ID), mustPgUUID(revision.ID), source.Generation)
	if err != nil {
		return nil, gitOpsIntentSnapshot{}, revision, mapErr(err)
	}
	var appliedSteps []EnvironmentGitOpsStep
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, gitOpsIntentSnapshot{}, revision, mapErr(err)
		}
		var steps []EnvironmentGitOpsStep
		if json.Unmarshal(raw, &steps) == nil {
			appliedSteps = append(appliedSteps, steps...)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, gitOpsIntentSnapshot{}, revision, mapErr(err)
	}
	rows.Close()
	inputs, err := workloadCandidateInputs(source, revision, desired, snapshot, plan, appliedSteps)
	if err != nil {
		return nil, gitOpsIntentSnapshot{}, revision, fmt.Errorf("environment workload candidate inputs: %w", err)
	}
	return inputs, snapshot, revision, nil
}

func (s *PgStore) PrepareEnvironmentGitOpsImageCandidates(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]EnvironmentWorkloadCandidate, error) {
	return s.PrepareEnvironmentGitOpsCandidates(ctx, lease, reviewed, nil)
}

func (s *PgStore) PrepareEnvironmentGitOpsCandidates(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan, artifacts map[string]EnvironmentWorkloadSourceArtifact) ([]EnvironmentWorkloadCandidate, error) {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	inputs, snapshot, revision, err := s.environmentCandidateInputsTx(ctx, tx, lease, reviewed)
	if err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return []EnvironmentWorkloadCandidate{}, nil
	}
	if _, err := q.SetEnvironmentGitOpsLeaseContext(ctx, tx, lease.LeaseToken); err != nil {
		return nil, mapErr(err)
	}
	out := make([]EnvironmentWorkloadCandidate, 0, len(inputs))
	for _, input := range inputs {
		frozen := candidateFrozenInputs(input)
		id, err := q.EnvironmentGitOpsCandidateByInput(ctx, tx, sqlc.EnvironmentGitOpsCandidateByInputParams{
			SourceID: lease.Source.ID, Generation: strconv.FormatInt(lease.Source.Generation, 10), Resource: frozen.Resource, PlanHash: reviewed.Hash})
		if errors.Is(err, pgx.ErrNoRows) {
			input, err = attachCandidateSource(input, artifacts, snapshot.Plan)
			if err != nil {
				return nil, err
			}
			id, err = q.CreateEnvironmentGitOpsWorkloadCandidate(ctx, tx, sqlc.CreateEnvironmentGitOpsWorkloadCandidateParams{
				AppID: mustPgUUID(input.AppID), Scope: input.Scope, Kind: string(input.Kind), Image: input.ImageDigest, CommitSha: input.CommitSHA, Runtime: []byte(input.EnvironmentWorkloadRuntime),
				SourcePath: input.SourcePath, SourceRoot: input.SourceRoot, SourceSha256: input.SourceSHA256, SourceBytes: input.SourceBytes, SourceUrl: input.SourceURL})
			if err == nil && input.Kind != DeploymentKindImage {
				err = q.CreateEnvironmentGitOpsSourceBuild(ctx, tx, sqlc.CreateEnvironmentGitOpsSourceBuildParams{
					DeploymentID: id, BuildID: mustPgUUID(input.BuildID), SourceBytes: input.SourceBytes, LogPath: artifacts[frozen.Resource].LogPath})
			}
			if err == nil && input.Kind == DeploymentKindImage {
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
		var storedRuntime []byte
		if err := tx.QueryRow(ctx, `SELECT environment_workload_runtime FROM deployments WHERE id=$1`, id).Scan(&storedRuntime); err != nil {
			return nil, mapErr(err)
		}
		if !frozenCandidateInputsMatch(storedRuntime, frozen) {
			return nil, fmt.Errorf("%w: stored candidate inputs differ from the reviewed intent", ErrConflict)
		}
		out = append(out, EnvironmentWorkloadCandidate{DeploymentID: pgUUIDString(dep.ID), BuildID: dep.BuildID, AppID: pgUUIDString(dep.AppID), Resource: frozen.Resource,
			Status: DeploymentStatus(dep.Status), HasRootfs: dep.RootfsPath != "" || dep.RootfsKey != ""})
	}
	graph, err := preparationGraph(snapshot, revision, lease.Source.Generation, reviewed.Hash, out)
	if err != nil {
		return nil, err
	}
	members, _ := json.Marshal(graph.Members)
	resourceIDs, _ := json.Marshal(graph.ResourceIDs)
	if err := q.CreateEnvironmentWorkloadGraph(ctx, tx, sqlc.CreateEnvironmentWorkloadGraphParams{
		SourceID: mustPgUUID(graph.SourceID), EnvironmentID: mustPgUUID(graph.EnvironmentID), RevisionID: mustPgUUID(graph.RevisionID),
		Generation: graph.Generation, IntentVersion: graph.IntentVersion, PlanHash: graph.PlanHash, DefinitionDigest: graph.DefinitionDigest,
		Members: members, ResourceIds: resourceIDs}); err != nil {
		return nil, mapErr(err)
	}
	// A long preparation must not commit after its issued lease expires.
	if err := lockEnvironmentGitOps(ctx, tx, lease, time.Now()); err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) EnvironmentGitOpsSourceRequests(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]EnvironmentWorkloadSourceRequest, error) {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	inputs, _, _, err := s.environmentCandidateInputsTx(ctx, tx, lease, reviewed)
	if err != nil {
		return nil, err
	}
	var requests []EnvironmentWorkloadSourceRequest
	for _, input := range inputs {
		if input.Kind == DeploymentKindImage {
			continue
		}
		frozen := candidateFrozenInputs(input)
		id, err := sqlc.New().EnvironmentGitOpsCandidateByInput(ctx, tx, sqlc.EnvironmentGitOpsCandidateByInputParams{
			SourceID: lease.Source.ID, Generation: strconv.FormatInt(lease.Source.Generation, 10), Resource: frozen.Resource, PlanHash: reviewed.Hash})
		if err == nil {
			var storedRuntime []byte
			if err := tx.QueryRow(ctx, `SELECT environment_workload_runtime FROM deployments WHERE id=$1`, id).Scan(&storedRuntime); err != nil {
				return nil, mapErr(err)
			}
			if !frozenCandidateInputsMatch(storedRuntime, frozen) {
				return nil, ErrConflict
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		request, err := sourceRequest(input)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	if err := lockEnvironmentGitOps(ctx, tx, lease, time.Now()); err != nil {
		return nil, err
	}
	return requests, tx.Commit(ctx)
}
