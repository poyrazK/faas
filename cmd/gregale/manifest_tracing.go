package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// Tracing, like profiling, is baked into the guest manifest by imaged, so it
// must be staged before the deployment is submitted (ADR-957). Failed
// submissions/builds restore the previous setting unless another client has
// changed it since.
type manifestTracingTransaction struct {
	client            manifestScalingClient
	slug              string
	previous, desired *api.TracingConfig
	committed         bool
}

func stageManifestTracing(ctx context.Context, client manifestScalingClient, slug, dir string) (*manifestTracingTransaction, error) {
	txn := &manifestTracingTransaction{client: client, slug: slug}
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
	if m.Lifecycle == nil || m.Lifecycle.Tracing == nil {
		return txn, nil
	}
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return txn, fmt.Errorf("read tracing policy: %w", err)
	}
	desired := *m.Lifecycle.Tracing
	if app.Manifest.Tracing != nil && *app.Manifest.Tracing == desired {
		return txn, nil
	}
	previous := api.TracingConfig{}
	if app.Manifest.Tracing != nil {
		previous = *app.Manifest.Tracing
	}
	if _, err := client.UpdateApp(ctx, slug, api.UpdateAppRequest{Tracing: &desired}); err != nil {
		return txn, fmt.Errorf("stage tracing policy: %w", err)
	}
	txn.previous, txn.desired = &previous, &desired
	return txn, nil
}

func (t *manifestTracingTransaction) rollback(ctx context.Context) error {
	if t.committed || t.desired == nil {
		return nil
	}
	app, err := t.client.GetApp(ctx, t.slug)
	if err != nil {
		return fmt.Errorf("read tracing policy for rollback: %w", err)
	}
	if app.Manifest.Tracing == nil || *app.Manifest.Tracing != *t.desired {
		return nil
	}
	if _, err := t.client.UpdateApp(ctx, t.slug, api.UpdateAppRequest{Tracing: t.previous}); err != nil {
		return fmt.Errorf("restore tracing policy: %w", err)
	}
	return nil
}
