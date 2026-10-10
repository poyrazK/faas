package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// Profiling must be set before submitting a deployment: imaged bakes app
// defaults into its guest manifest. Compensate failed submissions/builds and
// preserve later configuration edits made by another client.
type manifestProfilingTransaction struct {
	client            manifestScalingClient
	slug              string
	previous, desired *api.ProfilingConfig
	committed         bool
}

func stageManifestProfiling(ctx context.Context, client manifestScalingClient, slug, dir string) (*manifestProfilingTransaction, error) {
	txn := &manifestProfilingTransaction{client: client, slug: slug}
	if dir == "" {
		return txn, nil
	}
	m, ok, err := gregalemanifest.Load(dir)
	if err != nil {
		return txn, err
	}
	if !ok || m == nil {
		return txn, nil
	}
	if err := m.Validate(); err != nil {
		return txn, err
	}
	if m.Lifecycle == nil || m.Lifecycle.Profiling == nil {
		return txn, nil
	}
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return txn, fmt.Errorf("read profiling policy: %w", err)
	}
	desired := *m.Lifecycle.Profiling
	if app.Manifest.Profiling != nil && app.Manifest.Profiling.Equal(&desired) {
		return txn, nil
	}
	previous := api.ProfilingConfig{}
	if app.Manifest.Profiling != nil {
		previous = *app.Manifest.Profiling
	}
	if _, err := client.UpdateApp(ctx, slug, api.UpdateAppRequest{Profiling: &desired}); err != nil {
		return txn, fmt.Errorf("stage profiling policy: %w", err)
	}
	txn.previous, txn.desired = &previous, &desired
	return txn, nil
}

func (t *manifestProfilingTransaction) rollback(ctx context.Context) error {
	if t.committed || t.desired == nil {
		return nil
	}
	app, err := t.client.GetApp(ctx, t.slug)
	if err != nil {
		return fmt.Errorf("read profiling policy for rollback: %w", err)
	}
	if app.Manifest.Profiling == nil || !app.Manifest.Profiling.Equal(t.desired) {
		return nil
	}
	if _, err := t.client.UpdateApp(ctx, t.slug, api.UpdateAppRequest{Profiling: t.previous}); err != nil {
		return fmt.Errorf("restore profiling policy: %w", err)
	}
	return nil
}
