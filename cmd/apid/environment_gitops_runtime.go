package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

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
	if lease.Source.Spec.Mode == "enforce" {
		if err := store.EnsureEnvironmentGitOpsRuntime(ctx, lease, plan); err != nil {
			return false, err
		}
		if err := b.prepareWorkloadCandidates(ctx, lease, plan); err != nil {
			return false, err
		}
		if err := b.recoverRuntime(ctx, lease); err != nil {
			return false, err
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
	return len(pending) == 0, err
}

func (b *environmentGitOpsBackend) prepareWorkloadCandidates(ctx context.Context, lease state.EnvironmentGitOpsLease, plan environmentsync.Plan) error {
	managed := false
	for _, change := range plan.Changes {
		managed = managed || change.Action != "retain_unmanaged" && (change.Path == "source" || strings.HasPrefix(change.Path, "runtime/"))
	}
	if !managed {
		return nil
	}
	preparer, ok := b.intent.(state.EnvironmentGitOpsPreparationStore)
	if !ok {
		return nil // the runtime verifier continues reporting the graph unqualified
	}
	candidates, err := preparer.PrepareEnvironmentGitOpsImageCandidates(ctx, lease, plan)
	if errors.Is(err, state.ErrEnvironmentWorkloadPreparationUnavailable) {
		return nil // unsupported adapters retain partial status; never serving proof
	}
	if err != nil {
		return err
	}
	// PgStore also commits the durable handoff with candidate creation. A
	// repeated pending hint is safe: imaging deduplicates on deployment status.
	for _, candidate := range candidates {
		if candidate.Status != state.DeployPending {
			continue
		}
		payload, _ := json.Marshal(map[string]string{"app_id": candidate.AppID, "to": candidate.DeploymentID, "deployment_id": candidate.DeploymentID, "kind": "image"})
		if err := b.server.notif.Notify(ctx, db.NotifyEnvironmentWorkloadImage, string(payload)); err != nil {
			return err
		}
	}
	return nil
}
