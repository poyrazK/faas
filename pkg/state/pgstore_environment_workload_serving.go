package state

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsWorkloadServingStore = (*PgStore)(nil)
var _ EnvironmentGitOpsQueueServingAckStore = (*PgStore)(nil)

// CompleteEnvironmentQueueDeliveryClaims commits a successful named queue
// delivery and its optional GitOps serving evidence in one transaction. The
// serving acknowledgement is supplementary: deliveries outside an active,
// reviewed push-worker graph still complete normally.
func (s *PgStore) CompleteEnvironmentQueueDeliveryClaims(ctx context.Context, appID, triggerID, source string,
	ids []string, attempts []int32, replayGenerations []int64,
	acknowledgements []EnvironmentWorkloadQueueServingAcknowledgement) ([]string, error) {
	if !canonicalStateUUID(appID) || !canonicalStateUUID(triggerID) || source != "queue" || len(ids) == 0 ||
		len(ids) != len(attempts) || len(ids) != len(replayGenerations) {
		return nil, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	finalized, err := sqlc.New().QueueFinishDeliveryClaims(ctx, tx, sqlc.QueueFinishDeliveryClaimsParams{
		Ids: ids, Attempts: attempts, ReplayGenerations: replayGenerations, AppID: mustPgUUID(appID),
		Source: source, InvocationState: "completed", RecordState: "succeeded",
		Outcome: pgtype.Text{String: "success", Valid: true}, Result: []byte(`{"trigger_dispatch":"succeeded"}`),
		TriggerID: mustPgUUID(triggerID),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	finalizedSet := make(map[string]struct{}, len(finalized))
	for _, id := range finalized {
		finalizedSet[id] = struct{}{}
	}
	for _, acknowledgement := range acknowledgements {
		if _, completed := finalizedSet[acknowledgement.InvocationID]; !completed {
			continue
		}
		if acknowledgement.Mode != "push" || !canonicalStateUUID(acknowledgement.AppID) || !canonicalStateUUID(acknowledgement.BindingID) ||
			!canonicalStateUUID(acknowledgement.TriggerID) || !canonicalStateUUID(acknowledgement.DeploymentID) ||
			!canonicalStateUUID(acknowledgement.InvocationID) || strings.TrimSpace(acknowledgement.Scope) == "" {
			continue
		}
		if err := recordEnvironmentGitOpsQueueServingAcknowledgementTx(ctx, tx, acknowledgement); err != nil && !errors.Is(err, ErrConflict) {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	return finalized, nil
}

// CompleteEnvironmentGitOpsPullQueueDelivery completes a claimed generic
// queue invocation and its optional GitOps serving acknowledgement in the
// same transaction. The explicit attempt fences stale workers after lease
// expiry; only pull bindings can produce an acknowledgement.
func (s *PgStore) CompleteEnvironmentGitOpsPullQueueDelivery(ctx context.Context, invocationID string, attempt int,
	deploymentID string, result json.RawMessage) error {
	if !canonicalStateUUID(invocationID) || !canonicalStateUUID(deploymentID) || attempt <= 0 {
		return ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	invocation, err := s.completeInvocationTx(ctx, tx, invocationID, attempt, result, true)
	if err != nil {
		return mapErr(err)
	}
	if invocation.Source != InvocationQueue || invocation.QueueBindingID == "" || invocation.DeploymentScope == "" {
		return ErrConflict
	}
	acknowledgement := EnvironmentWorkloadQueueServingAcknowledgement{Mode: "pull", AppID: invocation.AppID,
		Scope: invocation.DeploymentScope, BindingID: invocation.QueueBindingID, DeploymentID: deploymentID,
		InvocationID: invocation.ID}
	if err := recordEnvironmentGitOpsQueueServingAcknowledgementTx(ctx, tx, acknowledgement); err != nil && !errors.Is(err, ErrConflict) {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return mapErr(err)
	}
	return nil
}

func (s *PgStore) RecordEnvironmentGitOpsQueueServingAcknowledgement(ctx context.Context,
	acknowledgement EnvironmentWorkloadQueueServingAcknowledgement) error {
	if !canonicalStateUUID(acknowledgement.AppID) || !canonicalStateUUID(acknowledgement.BindingID) ||
		(acknowledgement.Mode == "push" && !canonicalStateUUID(acknowledgement.TriggerID)) ||
		(acknowledgement.Mode == "pull" && acknowledgement.TriggerID != "") ||
		(acknowledgement.Mode != "push" && acknowledgement.Mode != "pull") || !canonicalStateUUID(acknowledgement.DeploymentID) ||
		!canonicalStateUUID(acknowledgement.InvocationID) || strings.TrimSpace(acknowledgement.Scope) == "" {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := recordEnvironmentGitOpsQueueServingAcknowledgementTx(ctx, tx, acknowledgement); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return mapErr(err)
	}
	return nil
}

func recordEnvironmentGitOpsQueueServingAcknowledgementTx(ctx context.Context, tx pgx.Tx,
	acknowledgement EnvironmentWorkloadQueueServingAcknowledgement) error {
	var graphID pgtype.UUID
	err := tx.QueryRow(ctx, `INSERT INTO environment_workload_serving_queue_acks
		(graph_id,binding_id,mode,trigger_id,app_id,deployment_id,invocation_id)
		SELECT graph.id,binding.id,$7,queue_trigger.id,binding.app_id,$5::uuid,$6::uuid
		FROM environment_workload_graphs graph
		JOIN active_environment_git_sources source ON source.id=graph.source_id
		JOIN project_environments environment ON environment.id=source.environment_id
		CROSS JOIN LATERAL jsonb_array_elements(graph.members) AS graph_member(value)
		CROSS JOIN LATERAL jsonb_each(coalesce(graph_member.value->'queue_bindings','{}'::jsonb)) AS queue_binding(name,value)
		JOIN queue_bindings binding ON binding.id=$3::uuid AND binding.app_id=$1::uuid
			AND binding.account_id=source.account_id AND binding.environment_id=source.environment_id
			AND binding.deployment_scope=environment.slug
			AND binding.name=queue_binding.name AND binding.enabled AND binding.retired_at IS NULL AND binding.mode=$7
		LEFT JOIN triggers queue_trigger ON queue_trigger.id=NULLIF($4,'')::uuid AND queue_trigger.account_id=source.account_id AND queue_trigger.app_id=binding.app_id
			AND queue_trigger.queue_binding_id=binding.id AND queue_trigger.enabled AND queue_trigger.kind='queue'
			AND queue_trigger.source='queue' AND queue_trigger.queue_binding_scope=environment.slug
			AND queue_trigger.queue_binding_environment_id=source.environment_id
		WHERE graph.phase='prepared' AND source.mode='enforce' AND NOT source.suspended
			AND source.generation=graph.generation AND source.intent_version=graph.intent_version
			AND source.approved_revision_id=graph.revision_id AND environment.slug=$2::text
		AND ((graph_member.value->>'execution_mode'='worker' AND binding.workload_class='worker') OR
				(graph_member.value->>'execution_mode'='request' AND graph_member.value->>'function'='true'
					AND binding.workload_class='http'))
			AND graph_member.value->>'app_id'=$1::text
			AND queue_binding.value->>'binding_id'=binding.id::text
			AND queue_binding.value->'contract'->>'queue_name'=binding.queue_name
			AND queue_binding.value->'contract'->>'mode'=binding.mode
			AND queue_binding.value->'contract'->>'workload_class'=binding.workload_class
			AND queue_binding.value->'contract'->>'enabled'='true'
			AND queue_binding.value->'contract'->>'max_concurrency'=binding.max_concurrency::text
			AND coalesce(nullif(queue_binding.value->'contract'->'retry_policy','null'::jsonb),'{}'::jsonb)=binding.retry_policy
			AND (($7='push' AND queue_trigger.id IS NOT NULL
				AND queue_binding.value->>'trigger_id'=queue_trigger.id::text
				AND EXISTS (SELECT 1 FROM trigger_records record WHERE record.trigger_id=queue_trigger.id
					AND record.item_identifier=$6 AND record.state='succeeded')) OR
				($7='pull' AND queue_trigger.id IS NULL
				AND coalesce(queue_binding.value->>'trigger_id','')=''))
			AND (graph_member.value->>'candidate_deployment_id'=$5::text OR
				(nullif(graph_member.value->>'candidate_deployment_id','') IS NULL AND
				 coalesce(graph_member.value->'retained_deployments','[]'::jsonb) ? $5::text))
		ON CONFLICT (graph_id,binding_id) DO UPDATE
			SET acknowledged_at=environment_workload_serving_queue_acks.acknowledged_at
			WHERE environment_workload_serving_queue_acks.app_id=EXCLUDED.app_id
				AND environment_workload_serving_queue_acks.mode=EXCLUDED.mode
				AND environment_workload_serving_queue_acks.trigger_id IS NOT DISTINCT FROM EXCLUDED.trigger_id
				AND environment_workload_serving_queue_acks.deployment_id=EXCLUDED.deployment_id
		RETURNING graph_id`, acknowledgement.AppID, acknowledgement.Scope, acknowledgement.BindingID,
		acknowledgement.TriggerID, acknowledgement.DeploymentID, acknowledgement.InvocationID, acknowledgement.Mode).Scan(&graphID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return mapErr(err)
	}
	if err := completeEnvironmentWorkloadServingReceiptIfReady(ctx, tx, graphID); err != nil {
		return err
	}
	return nil
}

func (s *PgStore) PrepareEnvironmentGitOpsWorkloadServing(ctx context.Context, lease EnvironmentGitOpsLease,
	reviewed environmentsync.Plan, gateways []string) (EnvironmentWorkloadServingReceipt, bool, error) {
	gateways = canonicalEnvironmentServingGateways(gateways)
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := sqlc.New().LockEnvironmentGitOpsServingProject(ctx, tx, sqlc.LockEnvironmentGitOpsServingProjectParams{
		ProjectID: mustPgUUID(lease.Source.ProjectID), AccountID: mustPgUUID(lease.Source.AccountID),
	}); err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
	}
	if _, _, _, err := s.environmentCandidateInputsTx(ctx, tx, lease, reviewed); err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	graphRow, err := sqlc.New().EnvironmentWorkloadGraphForPreparation(ctx, tx, sqlc.EnvironmentWorkloadGraphForPreparationParams{
		SourceID: mustPgUUID(lease.Source.ID), Generation: lease.Source.Generation, PlanHash: reviewed.Hash,
	})
	if err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
	}
	graph := workloadGraphFromSQL(graphRow)
	evidence, err := environmentWorkloadActivationEvidenceTx(ctx, tx, graphRow)
	if err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	if !evidence.Qualified || !evidence.Activated || !environmentGraphSupportsProductionServing(graph) {
		return EnvironmentWorkloadServingReceipt{}, false, nil
	}
	jobsReady, err := environmentGitOpsScheduledJobsReadyTx(ctx, tx, graph, "active", false)
	if err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	if !jobsReady {
		return EnvironmentWorkloadServingReceipt{}, false, nil
	}
	activeTargets, err := environmentGraphActiveReleaseTargetsTx(ctx, tx, graph)
	if err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	release, err := activeProjectReleaseSetTx(ctx, tx, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
	if err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	consumers, ok := environmentGraphServingQueueConsumers(graph, activeTargets)
	if !ok {
		return EnvironmentWorkloadServingReceipt{}, false, nil
	}
	routes := make([]EnvironmentWorkloadServingRoute, 0, len(graph.Members))
	for _, member := range graph.Members {
		deploymentID := activeTargets[member.Resource]
		if deploymentID == "" {
			return EnvironmentWorkloadServingReceipt{}, false, nil
		}
		if member.ExecutionMode == api.ExecutionModeRequest || member.ExecutionMode == api.ExecutionModeService {
			routes = append(routes, EnvironmentWorkloadServingRoute{AppID: member.AppID, DeploymentID: deploymentID,
				Cutover: member.CandidateDeploymentID != ""})
		}
	}
	scheduledJobs := environmentGitOpsScheduledJobMembers(graph)
	if len(routes) == 0 && len(consumers) == 0 && len(scheduledJobs) == 0 || len(routes) > 0 && len(gateways) == 0 {
		return EnvironmentWorkloadServingReceipt{}, false, nil
	}
	receiptGateways := gateways
	if len(routes) == 0 {
		receiptGateways = []string{}
	}
	slices.SortFunc(routes, func(a, b EnvironmentWorkloadServingRoute) int { return strings.Compare(a.AppID, b.AppID) })
	q := sqlc.New()
	if _, err := q.SetEnvironmentGitOpsServingContext(ctx, tx, lease.LeaseToken); err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
	}
	graphID := mustPgUUID(graph.ID)
	receipt, exists, err := loadEnvironmentWorkloadServingReceipt(ctx, tx, graphID)
	if err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	if exists {
		if receipt.SourceID != lease.Source.ID || receipt.ReleaseSetID != release.ID || !sameServingRoutes(receipt.Routes, routes) {
			return EnvironmentWorkloadServingReceipt{}, false, ErrConflict
		}
		resetRoutes := len(routes) > 0 && !slices.Equal(receipt.ExpectedGateways, receiptGateways) || len(routes) == 0 && len(receipt.ExpectedGateways) != 0
		cutoverWeightsMatch, err := q.EnvironmentWorkloadServingCutoverWeightsMatch(ctx, tx, graphID)
		if err != nil {
			return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
		}
		if !cutoverWeightsMatch.Valid || !cutoverWeightsMatch.Bool {
			resetRoutes = true
		}
		if resetRoutes {
			if _, err := q.ResetEnvironmentWorkloadServingReceipt(ctx, tx, sqlc.ResetEnvironmentWorkloadServingReceiptParams{
				GraphID: graphID, ReleaseSetID: mustPgUUID(release.ID), ExpectedGateways: receiptGateways,
			}); err != nil {
				return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
			}
			if err := q.DeleteEnvironmentWorkloadServingAcks(ctx, tx, graphID); err != nil {
				return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
			}
			exists = false // replace the route generations below
		}
	} else {
		if _, err := q.InsertEnvironmentWorkloadServingReceipt(ctx, tx, sqlc.InsertEnvironmentWorkloadServingReceiptParams{
			GraphID: graphID, SourceID: mustPgUUID(lease.Source.ID), ReleaseSetID: mustPgUUID(release.ID),
			SourceGeneration: lease.Source.Generation, IntentVersion: lease.Source.IntentVersion,
			RevisionID: mustPgUUID(lease.Revision.ID), PlanHash: reviewed.Hash, ExpectedGateways: receiptGateways,
		}); err != nil {
			return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
		}
	}
	if !exists {
		for i := range routes {
			generation, err := q.NextDeploymentRouteGenerationTx(ctx, tx)
			if err != nil {
				return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
			}
			routes[i].Generation = generation
			if err := q.UpsertEnvironmentWorkloadServingRoute(ctx, tx, sqlc.UpsertEnvironmentWorkloadServingRouteParams{
				GraphID: graphID, AppID: mustPgUUID(routes[i].AppID), DeploymentID: mustPgUUID(routes[i].DeploymentID),
				RouteGeneration: generation, CutoverRequired: routes[i].Cutover,
			}); err != nil {
				return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
			}
		}
	}
	// Validate every app's live siblings before changing any weights. A normal
	// canary/service rollout retains traffic ownership until its own handoff
	// finishes; GitOps cannot race that owner.
	for _, route := range routes {
		if !route.Cutover {
			continue
		}
		deployments, err := q.LiveEnvironmentWorkloadDeploymentsForServing(ctx, tx, sqlc.LiveEnvironmentWorkloadDeploymentsForServingParams{
			AppID: mustPgUUID(route.AppID), Scope: normalizedDeploymentScope(lease.Source.EnvironmentSlug),
		})
		if err != nil {
			return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
		}
		foundTarget := false
		for _, deployment := range deployments {
			if deployment.ID == route.DeploymentID {
				foundTarget = !deployment.EnvironmentWorkloadHeld
			}
			if deployment.ID != route.DeploymentID && NormalizeRolloutState(deployment.RolloutState) == "rolling_out" {
				return EnvironmentWorkloadServingReceipt{}, false, nil
			}
		}
		if !foundTarget {
			return EnvironmentWorkloadServingReceipt{}, false, ErrConflict
		}
	}
	for _, route := range routes {
		if !route.Cutover {
			continue
		}
		deployments, err := q.LiveEnvironmentWorkloadDeploymentsForServing(ctx, tx, sqlc.LiveEnvironmentWorkloadDeploymentsForServingParams{
			AppID: mustPgUUID(route.AppID), Scope: normalizedDeploymentScope(lease.Source.EnvironmentSlug),
		})
		if err != nil {
			return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
		}
		for _, deployment := range deployments {
			traffic := int32(0)
			if deployment.ID == route.DeploymentID {
				traffic = 100
			}
			if _, err := q.SetEnvironmentWorkloadServingTraffic(ctx, tx, sqlc.SetEnvironmentWorkloadServingTrafficParams{
				DeploymentID: mustPgUUID(deployment.ID), TrafficPercent: traffic,
			}); err != nil {
				return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
			}
		}
	}
	if err := completeEnvironmentWorkloadServingReceiptIfReady(ctx, tx, graphID); err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	receipt, exists, err = loadEnvironmentWorkloadServingReceipt(ctx, tx, graphID)
	if err != nil || !exists {
		if err == nil {
			err = ErrConflict
		}
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
	}
	return receipt, receipt.Serving, nil
}

func (s *PgStore) AcknowledgeEnvironmentGitOpsWorkloadServing(ctx context.Context, lease EnvironmentGitOpsLease,
	graphID string, generation int64, node string) (EnvironmentWorkloadServingReceipt, bool, error) {
	tx, err := s.gitOpsEffectTx(ctx, lease)
	if err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.SetEnvironmentGitOpsServingContext(ctx, tx, lease.LeaseToken); err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
	}
	pgGraphID := mustPgUUID(graphID)
	receipt, exists, err := loadEnvironmentWorkloadServingReceipt(ctx, tx, pgGraphID)
	if err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	if !exists || receipt.SourceID != lease.Source.ID || receipt.SourceGeneration != lease.Source.Generation ||
		receipt.RevisionID != lease.Revision.ID || !slices.Contains(receipt.ExpectedGateways, node) {
		return EnvironmentWorkloadServingReceipt{}, false, ErrConflict
	}
	if receipt.Serving {
		if err := tx.Commit(ctx); err != nil {
			return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
		}
		return receipt, true, nil
	}
	var routeFound bool
	for _, route := range receipt.Routes {
		if route.Generation == generation {
			routeFound = true
			break
		}
	}
	if !routeFound {
		return EnvironmentWorkloadServingReceipt{}, false, ErrConflict
	}
	if err := q.AcknowledgeEnvironmentWorkloadServingRoute(ctx, tx, sqlc.AcknowledgeEnvironmentWorkloadServingRouteParams{
		GraphID: pgGraphID, RouteGeneration: generation, NodeName: node,
	}); err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
	}
	if !slices.Contains(receipt.Acknowledgements[generation], node) {
		receipt.Acknowledgements[generation] = append(receipt.Acknowledgements[generation], node)
		slices.Sort(receipt.Acknowledgements[generation])
	}
	if err := completeEnvironmentWorkloadServingReceiptIfReady(ctx, tx, pgGraphID); err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	receipt, exists, err = loadEnvironmentWorkloadServingReceipt(ctx, tx, pgGraphID)
	if err != nil || !exists {
		if err == nil {
			err = ErrConflict
		}
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
	}
	return receipt, receipt.Serving, nil
}

func loadEnvironmentWorkloadServingReceipt(ctx context.Context, tx sqlc.DBTX, graphID pgtype.UUID) (EnvironmentWorkloadServingReceipt, bool, error) {
	q := sqlc.New()
	row, err := q.EnvironmentWorkloadServingReceipt(ctx, tx, graphID)
	if errors.Is(err, pgx.ErrNoRows) {
		return EnvironmentWorkloadServingReceipt{}, false, nil
	}
	if err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
	}
	receipt := EnvironmentWorkloadServingReceipt{GraphID: row.GraphID, SourceID: row.SourceID, SourceGeneration: row.SourceGeneration,
		IntentVersion: row.IntentVersion, RevisionID: row.RevisionID, PlanHash: row.PlanHash, ReleaseSetID: row.ReleaseSetID,
		ExpectedGateways: slices.Clone(row.ExpectedGateways), Acknowledgements: map[int64][]string{}, Serving: row.Phase == "served"}
	if row.ServedAt.Valid {
		servedAt := row.ServedAt.Time
		receipt.ServedAt = &servedAt
	}
	receiptRows, err := q.EnvironmentWorkloadServingRoutes(ctx, tx, graphID)
	if err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
	}
	for _, route := range receiptRows {
		receipt.Routes = append(receipt.Routes, EnvironmentWorkloadServingRoute{
			AppID: route.AppID, DeploymentID: route.DeploymentID, Generation: route.RouteGeneration, Cutover: route.CutoverRequired,
		})
	}
	ackRows, err := q.EnvironmentWorkloadServingAcks(ctx, tx, graphID)
	if err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
	}
	for _, ack := range ackRows {
		receipt.Acknowledgements[ack.RouteGeneration] = append(receipt.Acknowledgements[ack.RouteGeneration], ack.NodeName)
	}
	queueRows, err := tx.Query(ctx, `SELECT mode,app_id::text,binding_id::text,coalesce(trigger_id::text,''),deployment_id::text,invocation_id::text,acknowledged_at
		FROM environment_workload_serving_queue_acks WHERE graph_id=$1::uuid ORDER BY binding_id`, graphID)
	if err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
	}
	defer queueRows.Close()
	for queueRows.Next() {
		var acknowledgement EnvironmentWorkloadServingQueueAck
		if err := queueRows.Scan(&acknowledgement.Mode, &acknowledgement.AppID, &acknowledgement.BindingID, &acknowledgement.TriggerID,
			&acknowledgement.DeploymentID, &acknowledgement.InvocationID, &acknowledgement.Acknowledged); err != nil {
			return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
		}
		receipt.QueueAcknowledgements = append(receipt.QueueAcknowledgements, acknowledgement)
	}
	if err := queueRows.Err(); err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
	}
	if err := tx.QueryRow(ctx, `SELECT created_at FROM project_release_sets WHERE id=$1::uuid`, receipt.ReleaseSetID).Scan(&receipt.ReleaseCreatedAt); err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
	}
	var membersRaw []byte
	if err := tx.QueryRow(ctx, `SELECT members FROM environment_workload_graphs WHERE id=$1::uuid`, graphID).Scan(&membersRaw); err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, mapErr(err)
	}
	var members []EnvironmentWorkloadGraphMember
	if err := json.Unmarshal(membersRaw, &members); err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, ErrConflict
	}
	graph := EnvironmentWorkloadGraph{Members: members}
	receipt.ExpectedScheduledJobs = environmentGitOpsScheduledJobMembers(graph)
	receipt.ScheduledJobAcks, err = environmentGitOpsScheduledJobAcksTx(ctx, tx, receipt.GraphID, receipt.ReleaseSetID)
	if err != nil {
		return EnvironmentWorkloadServingReceipt{}, false, err
	}
	return receipt, true, nil
}

func completeEnvironmentWorkloadServingReceiptIfReady(ctx context.Context, tx pgx.Tx, graphID pgtype.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE environment_workload_serving_receipts
		SET phase='served',served_at=coalesce(served_at,clock_timestamp()),updated_at=clock_timestamp()
		WHERE graph_id=$1::uuid AND phase='pending' AND public.environment_workload_serving_ready($1::uuid)`, graphID)
	if err != nil {
		return mapErr(err)
	}
	return nil
}
