package main

import (
	"context"
	"encoding/json"

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
