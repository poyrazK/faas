package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// functionHookClient is the API surface applyFunctionHookLifecycle needs.
type functionHookClient interface {
	GetApp(ctx context.Context, slug string) (api.AppResponse, error)
	UpdateApp(ctx context.Context, slug string, req api.UpdateAppRequest) (api.AppResponse, error)
}

// applyFunctionHookLifecycle points a single function app's lifecycle at the
// runner for the snapshot hooks its handler declares, and clears hooks the
// code no longer declares (ADR-20261110190000 §5). It runs before the upload,
// so the deployment's first snapshot is taken with them.
func applyFunctionHookLifecycle(ctx context.Context, client functionHookClient, slug, sourceDir, handler string) error {
	spec := gregalemanifest.FunctionSpec{Name: slug, Hooks: gregalemanifest.FunctionHooksInDir(sourceDir, handler)}
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return fmt.Errorf("read app %s: %w", slug, err)
	}
	after, before, changed, err := spec.HookLifecycleUpdate(app.Manifest.AfterRestore, app.Manifest.BeforeCheckpoint)
	if err != nil || !changed {
		return err
	}
	if _, err := client.UpdateApp(ctx, slug, api.UpdateAppRequest{AfterRestore: after, BeforeCheckpoint: before}); err != nil {
		return fmt.Errorf("set snapshot hooks: %w", err)
	}
	return nil
}

// functionSetMemberKey marks the context of one function's deploy inside a
// functions-project deploy, which has already applied that function's hook
// lifecycle with its entry's timeouts before the build.
type functionSetMemberKey struct{}

func withFunctionSetMember(ctx context.Context) context.Context {
	return context.WithValue(ctx, functionSetMemberKey{}, true)
}

func isFunctionSetMember(ctx context.Context) bool {
	member, _ := ctx.Value(functionSetMemberKey{}).(bool)
	return member
}
