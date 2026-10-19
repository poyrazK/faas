package state

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsActivationEvidenceStore = (*PgStore)(nil)
var _ EnvironmentGitOpsCurrentWorkloadEvidenceStore = (*PgStore)(nil)

func (s *PgStore) EnvironmentGitOpsActivationEvidence(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) (EnvironmentWorkloadActivationEvidence, error) {
	var zero EnvironmentWorkloadActivationEvidence
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, _, _, err := s.environmentCandidateInputsTx(ctx, tx, lease, reviewed); err != nil {
		return zero, err
	}
	row, err := sqlc.New().EnvironmentWorkloadGraphForPreparation(ctx, tx, sqlc.EnvironmentWorkloadGraphForPreparationParams{
		SourceID: mustPgUUID(lease.Source.ID), Generation: lease.Source.Generation, PlanHash: reviewed.Hash,
	})
	if err != nil {
		return zero, mapErr(err)
	}
	return environmentWorkloadActivationEvidenceTx(ctx, tx, row)
}

// CurrentEnvironmentGitOpsWorkloadEvidence reads only the graph for the source's
// current approved plan. The repeatable-read, read-only transaction keeps the
// source, intent, graph, and receipts on one snapshot without acquiring a lease
// or row lock.
func (s *PgStore) CurrentEnvironmentGitOpsWorkloadEvidence(ctx context.Context, accountID, sourceID string) (*EnvironmentWorkloadActivationEvidence, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return currentEnvironmentGitOpsWorkloadEvidenceTx(ctx, tx, accountID, sourceID)
}

func currentEnvironmentGitOpsWorkloadEvidenceTx(ctx context.Context, tx sqlc.DBTX, accountID, sourceID string) (*EnvironmentWorkloadActivationEvidence, error) {
	q := sqlc.New()
	rawSource, err := q.GetEnvironmentGitSourceByID(ctx, tx, mustPgUUID(sourceID))
	if errors.Is(err, pgx.ErrNoRows) || err == nil && pgUUIDString(rawSource.AccountID) != accountID {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, mapErr(err)
	}
	if rawSource.Detached || rawSource.Suspended || !rawSource.ApprovedRevisionID.Valid {
		return nil, nil
	}
	scope, err := q.GetEnvironmentGitOpsScope(ctx, tx, rawSource.ID)
	if err != nil {
		return nil, mapErr(err)
	}
	source := environmentGitSourceFromSQL(rawSource, scope.EnvironmentSlug)
	rawRevision, err := q.GetEnvironmentDesiredRevision(ctx, tx, sqlc.GetEnvironmentDesiredRevisionParams{
		SourceID: rawSource.ID, RevisionID: rawSource.ApprovedRevisionID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, mapErr(err)
	}
	revision := environmentRevisionFromSQL(rawRevision)
	desired, err := desiredEnvironmentRevision(revision)
	if err != nil {
		return nil, err
	}
	observed, _, err := readEnvironmentGitOpsIntent(ctx, tx, source, desired)
	if err != nil {
		return nil, err
	}
	plan, err := environmentGitOpsPlan(source, revision, desired, observed, false)
	if err != nil {
		return nil, err
	}
	row, err := q.EnvironmentWorkloadGraphForStatus(ctx, tx, sqlc.EnvironmentWorkloadGraphForStatusParams{
		SourceID: rawSource.ID, Generation: source.Generation, PlanHash: plan.Hash,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	if pgUUIDString(row.SourceID) != source.ID || pgUUIDString(row.EnvironmentID) != source.EnvironmentID ||
		pgUUIDString(row.RevisionID) != revision.ID || row.Generation != source.Generation ||
		row.IntentVersion != source.IntentVersion || row.DefinitionDigest != revision.Digest || row.PlanHash != plan.Hash {
		return nil, nil
	}
	evidence, err := environmentWorkloadActivationEvidenceTx(ctx, tx, row, true)
	if err != nil {
		return nil, err
	}
	evidence.CommitSHA = revision.CommitSHA
	return &evidence, nil
}

func environmentWorkloadActivationEvidenceTx(ctx context.Context, tx sqlc.DBTX, row sqlc.EnvironmentWorkloadGraph, diagnostics ...bool) (EnvironmentWorkloadActivationEvidence, error) {
	var zero EnvironmentWorkloadActivationEvidence
	q := sqlc.New()
	requests, err := q.EnvironmentWorkloadQualificationsByGraph(ctx, tx, row.ID)
	if err != nil {
		return zero, mapErr(err)
	}
	captures := map[string]EnvironmentQualificationSnapshotReceipt{}
	restores := map[string]EnvironmentQualificationRestoreReceipt{}
	smokes := map[string]EnvironmentQualificationSmokeReceipt{}
	jobSmokes := map[string]EnvironmentQualificationJobSmokeReceipt{}
	configs := map[string]EnvironmentQualificationConfigReceipt{}
	frameworkReady := map[string]EnvironmentQualificationFrameworkReadyReceipt{}
	for _, request := range requests {
		if !request.ReservedInstanceID.Valid {
			continue
		}
		current, err := q.EnvironmentWorkloadQualificationEvidenceCurrent(ctx, tx, request.ID)
		if err != nil {
			return zero, mapErr(err)
		}
		if !current {
			continue
		}
		captured := qualificationRequestFromSQL(request)
		if captured.ExecutionMode == api.ExecutionModeJob {
			jobRow, jobErr := q.EnvironmentQualificationJobSmokeReceipt(ctx, tx, sqlc.EnvironmentQualificationJobSmokeReceiptParams{
				RequestID: request.ID, Attempt: request.Attempt,
			})
			if errors.Is(jobErr, pgx.ErrNoRows) {
				continue
			}
			if jobErr != nil {
				return zero, mapErr(jobErr)
			}
			jobReceipt := qualificationJobSmokeReceiptFromSQL(jobRow)
			execRow, execErr := q.EnvironmentQualificationExecution(ctx, tx, request.ReservedInstanceID)
			if execErr != nil {
				return zero, mapErr(execErr)
			}
			execution, execErr := qualificationExecutionFromSQL(execRow)
			instance, instanceErr := q.EnvironmentQualificationInstance(ctx, tx, request.ReservedInstanceID)
			if execErr != nil || instanceErr != nil || !execution.DispatchStarted || execution.RetiredAt == nil ||
				execution.Retirement == nil || execution.Retirement.Kind != QualificationNativeRetired ||
				!execution.Retirement.ProcessesExited || !execution.Retirement.ResourcesRemoved ||
				State(instance.State) != StateStopped || !qualificationJobSmokeReceiptMatchesRequest(jobReceipt, captured) {
				continue
			}
			configRow, configErr := q.EnvironmentQualificationConfigReceipt(ctx, tx, request.ReservedInstanceID)
			if configErr != nil {
				if errors.Is(configErr, pgx.ErrNoRows) {
					continue
				}
				return zero, mapErr(configErr)
			}
			config := qualificationConfigReceiptFromSQL(configRow)
			if !qualificationConfigReceiptMatchesFrame(config, captured, execution.Execution) {
				continue
			}
			configs[captured.ReservedInstanceID] = config
			jobSmokes[captured.DeploymentID] = jobReceipt
			continue
		}
		stored, err := q.EnvironmentQualificationSnapshotReceipt(ctx, tx, request.ReservedInstanceID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return zero, mapErr(err)
		}
		receipt, err := qualificationSnapshotReceiptFromSQL(stored)
		if err != nil {
			return zero, err
		}
		fresh, err := readRuntimeConfigInputsFresh(ctx, tx, captured.AppID, receipt.Inputs)
		if err != nil {
			return zero, err
		}
		if !fresh || receipt.Execution.RequestID != captured.ID || receipt.Execution.Attempt != captured.Attempt || receipt.Execution.Artifact != captured.Artifact {
			continue
		}
		captures[captured.DeploymentID] = receipt
		configRow, configErr := q.EnvironmentQualificationConfigReceipt(ctx, tx, request.ReservedInstanceID)
		if configErr == nil {
			configs[captured.ReservedInstanceID] = qualificationConfigReceiptFromSQL(configRow)
		} else if !errors.Is(configErr, pgx.ErrNoRows) {
			return zero, mapErr(configErr)
		}
		restored, err := q.EnvironmentQualificationRestoreReceipt(ctx, tx, sqlc.EnvironmentQualificationRestoreReceiptParams{
			RequestID: request.ID, Attempt: request.Attempt,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return zero, mapErr(err)
		}
		restore, err := qualificationRestoreReceiptFromSQL(restored)
		if err != nil {
			return zero, err
		}
		restoreFresh, err := readRuntimeConfigInputsFresh(ctx, tx, captured.AppID, restore.Inputs)
		if err != nil {
			return zero, err
		}
		if !restoreFresh || restore.RequestID != captured.ID || restore.Attempt != captured.Attempt ||
			restore.CaptureInstanceID != captured.ReservedInstanceID || restore.InstanceID == captured.ReservedInstanceID ||
			!qualificationRuntimeValuesEqual(restore.Inputs, receipt.Inputs) {
			continue
		}
		restores[captured.ReservedInstanceID] = restore
		restoredConfigRow, configErr := q.EnvironmentQualificationConfigReceipt(ctx, tx, mustPgUUID(restore.InstanceID))
		if configErr == nil {
			configs[restore.InstanceID] = qualificationConfigReceiptFromSQL(restoredConfigRow)
		} else if !errors.Is(configErr, pgx.ErrNoRows) {
			return zero, mapErr(configErr)
		}
		readyRow, readyErr := q.EnvironmentQualificationFrameworkReadyReceipt(ctx, tx, sqlc.EnvironmentQualificationFrameworkReadyReceiptParams{
			RequestID: request.ID, Attempt: request.Attempt,
		})
		ready := qualificationFrameworkReadyReceiptFromSQL(readyRow)
		if readyErr == nil && qualificationFrameworkReadyReceiptMatchesRestore(ready, restore, row.ID.String()) {
			frameworkReady[captured.ReservedInstanceID] = ready
		} else if readyErr != nil && !errors.Is(readyErr, pgx.ErrNoRows) {
			return zero, mapErr(readyErr)
		}
		smokeRow, err := q.EnvironmentQualificationSmokeReceipt(ctx, tx, sqlc.EnvironmentQualificationSmokeReceiptParams{
			RequestID: request.ID, Attempt: request.Attempt,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return zero, mapErr(err)
		}
		smoke := qualificationSmokeReceiptFromSQL(smokeRow)
		if qualificationSmokeReceiptMatchesRequest(smoke, captured, restore) {
			smokes[captured.ReservedInstanceID] = smoke
		}
	}
	graph := workloadGraphFromSQL(row)
	activeTargets, err := environmentGraphActiveReleaseTargetsTx(ctx, tx, graph)
	if err != nil {
		return zero, err
	}
	evidence := graphActivationEvidenceWithReleaseTargets(graph, captures, restores, smokes, configs, frameworkReady, jobSmokes, activeTargets)
	if evidence.Qualified && !evidence.Activated {
		var projectID, scope string
		source, err := q.GetEnvironmentGitSourceByID(ctx, tx, mustPgUUID(graph.SourceID))
		if err != nil {
			return zero, mapErr(err)
		}
		projectID = pgUUIDString(source.ProjectID)
		// The frozen scope is checked against the graph environment by the native
		// evidence reader before binding evidence is considered.
		if len(requests) > 0 {
			scope = qualificationRequestFromSQL(requests[0]).FrozenInputs.Scope
		}
		if _, err := checkEnvironmentBindingGraphTx(ctx, tx, projectID, scope, graph, candidateBindingGraphMembers(graph)); err != nil {
			if !IsBindingReleaseRequired(err) && !errors.Is(err, ErrBindingPromotionExpired) {
				return zero, err
			}
			evidence.BlockingReasons = append(evidence.BlockingReasons, candidateBindingBlocker(err))
		}
	}
	jobStatus := "paused"
	if evidence.Activated {
		jobStatus = "active"
	}
	scheduledJobsReady, err := environmentGitOpsScheduledJobsReadyTx(ctx, tx, graph, jobStatus, false)
	if err != nil {
		return zero, err
	}
	if !scheduledJobsReady {
		evidence.BlockingReasons = append(evidence.BlockingReasons, "environment_scheduled_job_materialization_unready")
		slices.Sort(evidence.BlockingReasons)
		evidence.BlockingReasons = slices.Compact(evidence.BlockingReasons)
	}
	serving, err := environmentWorkloadServingEvidenceTx(ctx, tx, graph, activeTargets)
	if err != nil {
		return zero, err
	}
	applyEnvironmentWorkloadServingEvidence(&evidence, serving)
	if len(diagnostics) > 0 && diagnostics[0] {
		evidence.Workloads, err = environmentWorkloadProgress(graph, captures, restores, smokes, configs, frameworkReady, jobSmokes, activeTargets, serving,
			func(member EnvironmentWorkloadGraphMember) (string, string, error) {
				return environmentWorkloadPreparationStatusTx(ctx, tx, member)
			})
		if err != nil {
			return zero, err
		}
		for i := range evidence.Workloads {
			progress := &evidence.Workloads[i]
			if progress.DeploymentID == "" {
				continue
			}
			dep, err := q.DeploymentByID(ctx, tx, mustPgUUID(progress.DeploymentID))
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return zero, mapErr(err)
			}
			buildID, _ := dep.Coalesce.(string)
			if buildID == "" || pgUUIDString(dep.AppID) != progress.AppID {
				continue
			}
			build, err := q.BuildByID(ctx, tx, mustPgUUID(buildID))
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return zero, mapErr(err)
			}
			if pgUUIDString(build.DeploymentID) == progress.DeploymentID {
				environmentBuildCacheProgress(progress, Build{CacheStatus: build.CacheStatus.String, CacheKeySHA256: build.CacheKeySha256.String})
			}
		}
	}
	return evidence, nil
}

func environmentWorkloadPreparationStatusTx(ctx context.Context, tx sqlc.DBTX, member EnvironmentWorkloadGraphMember) (string, string, error) {
	if member.CandidateDeploymentID == "" {
		return "", "", nil
	}
	q := sqlc.New()
	deployment, err := q.DeploymentByID(ctx, tx, mustPgUUID(member.CandidateDeploymentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", mapErr(err)
	}
	if pgUUIDString(deployment.AppID) != member.AppID {
		return "", "", nil
	}
	buildID, _ := deployment.Coalesce.(string)
	if buildID == "" {
		return deployment.Status, "", nil
	}
	build, err := q.BuildByID(ctx, tx, mustPgUUID(buildID))
	if errors.Is(err, pgx.ErrNoRows) {
		return deployment.Status, "", nil
	}
	if err != nil {
		return "", "", mapErr(err)
	}
	if pgUUIDString(build.DeploymentID) != member.CandidateDeploymentID {
		return deployment.Status, "", nil
	}
	return deployment.Status, build.Status, nil
}

func environmentWorkloadServingEvidenceTx(ctx context.Context, tx sqlc.DBTX, graph EnvironmentWorkloadGraph,
	activeTargets map[string]string) (bool, error) {
	q := sqlc.New()
	pgGraphID := mustPgUUID(graph.ID)
	receipt, exists, err := loadEnvironmentWorkloadServingReceipt(ctx, tx, pgGraphID)
	if err != nil || !exists {
		return false, err
	}
	releaseID, err := q.ActiveEnvironmentWorkloadReleaseSet(ctx, tx, pgGraphID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, mapErr(err)
	}
	gateways, err := q.ListEnvironmentServingGatewayNames(ctx, tx)
	if err != nil {
		return false, mapErr(err)
	}
	weights, err := q.EnvironmentWorkloadServingWeightsMatch(ctx, tx, pgGraphID)
	if err != nil {
		return false, mapErr(err)
	}
	return receipt.SourceID == graph.SourceID && receipt.SourceGeneration == graph.Generation && receipt.IntentVersion == graph.IntentVersion &&
		receipt.RevisionID == graph.RevisionID && receipt.PlanHash == graph.PlanHash && weights.Valid && weights.Bool &&
		environmentWorkloadServingReceiptMatches(receipt, graph, releaseID, gateways, activeTargets), nil
}

// Release IDs in the prepared graph are only hints. A member matches an
// activated graph only when the exact candidate or retained deployment is
// selected by the active release set for this environment and is live and
// unheld. This keeps the status read independent of stale graph JSON.
func environmentGraphActiveReleaseTargetsTx(ctx context.Context, tx sqlc.DBTX, graph EnvironmentWorkloadGraph) (map[string]string, error) {
	rows, err := sqlc.New().ActiveEnvironmentWorkloadReleaseMembers(ctx, tx, sqlc.ActiveEnvironmentWorkloadReleaseMembersParams{
		SourceID: mustPgUUID(graph.SourceID), EnvironmentID: mustPgUUID(graph.EnvironmentID),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	byApp := map[string]string{}
	for _, row := range rows {
		if existing := byApp[row.AppID]; existing != "" && existing != row.DeploymentID {
			return nil, ErrConflict
		}
		byApp[row.AppID] = row.DeploymentID
	}
	targets := make(map[string]string)
	for _, member := range graph.Members {
		deploymentID := byApp[member.AppID]
		if deploymentID != "" && (member.CandidateDeploymentID != "" && deploymentID == member.CandidateDeploymentID ||
			member.CandidateDeploymentID == "" && slices.Contains(member.RetainedDeployments, deploymentID)) {
			targets[member.Resource] = deploymentID
		}
	}
	return targets, nil
}
