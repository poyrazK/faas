package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

var _ environmentgitops.RuntimeVerifier = (*environmentGitOpsBackend)(nil)

func (b *environmentGitOpsBackend) runtimeStore() (state.EnvironmentGitOpsRuntimeStore, error) {
	store, ok := b.intent.(state.EnvironmentGitOpsRuntimeStore)
	if !ok {
		return nil, state.ErrConflict
	}
	return store, nil
}

func (b *environmentGitOpsBackend) recoverRuntime(ctx context.Context, lease state.EnvironmentGitOpsLease) error {
	store, err := b.runtimeStore()
	if err != nil {
		return err
	}
	pending, err := store.PendingEnvironmentGitOpsRuntime(ctx, lease)
	if err != nil {
		return err
	}
	for _, effect := range pending {
		progress, err := store.ReconcileEnvironmentGitOpsRuntime(ctx, lease, effect.ID)
		if err != nil {
			return err
		}
		if progress.Request != nil {
			raw, _ := json.Marshal(progress.Request)
			if err := b.server.notif.Notify(ctx, db.NotifyRuntimeConfigRestart, string(raw)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *environmentGitOpsBackend) VerifyRuntime(ctx context.Context, lease state.EnvironmentGitOpsLease, plan environmentsync.Plan) (bool, error) {
	store, err := b.runtimeStore()
	if err != nil {
		return false, err
	}
	workloadReady := true
	var workloadErr error
	if lease.Source.Spec.Mode == "enforce" {
		if err := store.EnsureEnvironmentGitOpsRuntime(ctx, lease, plan); err != nil {
			return false, err
		}
		workloadReady, workloadErr = b.prepareWorkloadCandidates(ctx, lease, plan)
		if err := b.recoverRuntime(ctx, lease); err != nil {
			return false, err
		}
		if workloadErr != nil {
			return false, workloadErr
		}
	}
	targets, err := store.ObserveEnvironmentGitOpsRuntime(ctx, lease)
	if err != nil {
		return false, err
	}
	for _, target := range targets {
		if !target.Ready() {
			return false, nil
		}
	}
	pending, err := store.PendingEnvironmentGitOpsRuntime(ctx, lease)
	if err != nil || len(pending) != 0 {
		return false, err
	}
	effects, err := b.effects.PendingEnvironmentGitOpsEffects(ctx, lease)
	return workloadReady && len(effects) == 0, err
}

func (b *environmentGitOpsBackend) prepareWorkloadCandidates(ctx context.Context, lease state.EnvironmentGitOpsLease, plan environmentsync.Plan) (bool, error) {
	preparer, ok := b.intent.(state.EnvironmentGitOpsPreparationStore)
	if !ok {
		return false, nil // without a preparation adapter, the graph is not serving evidence
	}
	requests, err := preparer.EnvironmentGitOpsSourceRequests(ctx, lease, plan)
	if errors.Is(err, state.ErrEnvironmentWorkloadPreparationUnavailable) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	artifacts := map[string]state.EnvironmentWorkloadSourceArtifact{}
	for _, request := range requests {
		artifact, err := b.server.prepareEnvironmentGitArchive(ctx, lease, request)
		if err != nil {
			return false, err
		}
		artifacts[request.Resource] = artifact
	}
	candidates, err := preparer.PrepareEnvironmentGitOpsCandidates(ctx, lease, plan, artifacts)
	if errors.Is(err, state.ErrEnvironmentWorkloadPreparationUnavailable) {
		return false, nil // unsupported adapters remain partial; never serving proof
	}
	if err != nil {
		return false, err
	}
	// PgStore also commits the durable handoff with candidate creation. A
	// repeated pending hint is safe: imaging deduplicates on deployment status.
	for _, candidate := range candidates {
		if candidate.BuildID != "" {
			if candidate.Status == state.DeployBuilding {
				payload, _ := json.Marshal(map[string]string{"build_id": candidate.BuildID, "app_id": candidate.AppID, "deployment_id": candidate.DeploymentID, "kind": "github"})
				if err := b.server.notif.Notify(ctx, db.NotifyBuildQueued, string(payload)); err != nil {
					return false, err
				}
			}
			continue
		}
		if candidate.Status != state.DeployPending {
			continue
		}
		payload, _ := json.Marshal(map[string]string{"app_id": candidate.AppID, "to": candidate.DeploymentID, "deployment_id": candidate.DeploymentID, "kind": "image"})
		if err := b.server.notif.Notify(ctx, db.NotifyEnvironmentWorkloadImage, string(payload)); err != nil {
			return false, err
		}
	}
	graphStore, ok := b.intent.(state.EnvironmentGitOpsGraphPreparationStore)
	if !ok {
		return false, nil
	}
	graph, err := graphStore.ReconcileEnvironmentGitOpsPreparation(ctx, lease, plan)
	if err != nil {
		return false, err
	}
	if graph.Phase == "prepared" {
		if err := b.queueWorkloadQualification(ctx, lease, plan); err != nil {
			return false, err
		}
		if activator, ok := b.intent.(state.EnvironmentGitOpsGraphActivationStore); ok {
			_, activated, err := activator.ActivateEnvironmentGitOpsWorkloadGraph(ctx, lease, plan)
			if err != nil {
				return false, err
			}
			if activated {
				return b.convergeEnvironmentGitOpsWorkloadServing(ctx, lease, plan)
			}
		}
	}
	// Qualification, activation and serving convergence remain independent
	// receipts. A prepared graph alone cannot complete runtime verification.
	return false, nil
}

func (b *environmentGitOpsBackend) convergeEnvironmentGitOpsWorkloadServing(ctx context.Context, lease state.EnvironmentGitOpsLease,
	plan environmentsync.Plan) (bool, error) {
	store, ok := b.intent.(state.EnvironmentGitOpsWorkloadServingStore)
	if !ok {
		return false, nil
	}
	gateways, err := b.server.servingEdgeRuleNodes(ctx)
	if err != nil {
		return false, err
	}
	receipt, serving, err := store.PrepareEnvironmentGitOpsWorkloadServing(ctx, lease, plan, gateways)
	if err != nil || serving {
		return serving, err
	}
	if receipt.GraphID == "" || len(receipt.Routes) == 0 || len(receipt.ExpectedGateways) == 0 {
		// Scheduled-Job-only graphs have no route acknowledgements. The store
		// retains their pending serving receipt and observes the next scheduled
		// occurrence on a later GitOps reconciliation tick.
		return false, nil
	}
	events, cancel, err := b.server.notif.Subscribe(ctx, []string{db.NotifyDeploymentRouteAck})
	if err != nil {
		return false, err
	}
	defer cancel()
	waitCtx, stop := context.WithTimeout(ctx, edgeRuleConvergenceTimeout)
	defer stop()
	retry := time.NewTicker(edgeRuleNotificationRetry)
	defer retry.Stop()
	routesByGeneration := make(map[int64]state.EnvironmentWorkloadServingRoute, len(receipt.Routes))
	for _, route := range receipt.Routes {
		routesByGeneration[route.Generation] = route
	}
	notifyPending := func() error {
		for _, route := range receipt.Routes {
			if slices.Equal(receipt.Acknowledgements[route.Generation], receipt.ExpectedGateways) {
				continue
			}
			payload, err := json.Marshal(db.DeploymentRouteChangedPayload{AppID: route.AppID, DeploymentID: route.DeploymentID, Generation: route.Generation})
			if err != nil {
				return err
			}
			if err := b.server.notif.Notify(waitCtx, db.NotifyDeploymentRouteChanged, string(payload)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := notifyPending(); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, err
	}
	for {
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, nil // durable receipt remains pending for the next lease
		case event, open := <-events:
			if !open {
				if ctx.Err() != nil {
					return false, ctx.Err()
				}
				return false, nil
			}
			ack, parseErr := db.ParseDeploymentRouteAckPayload(event.Payload)
			if parseErr != nil {
				continue
			}
			route, expectedGeneration := routesByGeneration[ack.Generation]
			if !expectedGeneration || !slices.Contains(receipt.ExpectedGateways, ack.Node) {
				continue
			}
			updated, complete, err := store.AcknowledgeEnvironmentGitOpsWorkloadServing(waitCtx, lease, receipt.GraphID, route.Generation, ack.Node)
			if err != nil {
				if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
					continue
				}
				return false, err
			}
			receipt = updated
			if complete {
				return true, nil
			}
		case <-retry.C:
			if err := notifyPending(); err != nil {
				if ctx.Err() != nil {
					return false, ctx.Err()
				}
				return false, err
			}
		}
	}
}

func (b *environmentGitOpsBackend) queueWorkloadQualification(ctx context.Context, lease state.EnvironmentGitOpsLease, plan environmentsync.Plan) error {
	store, ok := b.intent.(state.EnvironmentGitOpsQualificationStore)
	if !ok {
		return nil
	}
	requests, err := store.QueueEnvironmentGitOpsQualification(ctx, lease, plan)
	if err != nil {
		return err
	}
	for _, request := range requests {
		if request.Phase != "queued" && (request.LeaseUntil == nil || time.Now().Before(*request.LeaseUntil)) {
			continue
		}
		payload, _ := json.Marshal(map[string]string{"qualification_id": request.ID, "graph_id": request.GraphID, "app_id": request.AppID, "deployment_id": request.DeploymentID})
		if err := b.server.notif.Notify(ctx, db.NotifyEnvironmentWorkloadQualify, string(payload)); err != nil {
			return err
		}
	}
	return nil
}
