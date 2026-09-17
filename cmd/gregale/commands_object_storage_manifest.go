package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

type manifestObjectStorageClient interface {
	ListObjectBuckets(context.Context, string) (api.ObjectBucketList, error)
	ListObjectStorageComputeBindings(context.Context, string, string) (api.ObjectStorageComputeBindingList, error)
	CreateObjectStorageComputeBinding(context.Context, string, string, api.CreateObjectStorageComputeBindingRequest) (api.ObjectStorageComputeBinding, error)
}

// resolveManifestObjectBucket accepts either the stable bucket ID or the
// customer-facing logical name, scoped to the environment selected by the
// deployment. Buckets are app-owned, so the caller only needs the app-scoped
// catalog and never receives provider identifiers.
func resolveManifestObjectBucket(items []api.ObjectBucket, reference, scope string) (api.ObjectBucket, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return api.ObjectBucket{}, fmt.Errorf("bucket reference is required")
	}
	var match api.ObjectBucket
	matchedReference := false
	for _, candidate := range items {
		if candidate.State == "deleted" || (candidate.ID != reference && candidate.Name != reference) {
			continue
		}
		matchedReference = true
		if candidate.Scope != scope {
			continue
		}
		if match.ID != "" && candidate.ID != match.ID {
			return api.ObjectBucket{}, fmt.Errorf("bucket reference %q is ambiguous in scope %q; use its ID", reference, scope)
		}
		match = candidate
	}
	if match.ID == "" {
		if matchedReference {
			return api.ObjectBucket{}, fmt.Errorf("bucket %q does not exist in scope %q", reference, scope)
		}
		return api.ObjectBucket{}, fmt.Errorf("no object-storage bucket named or identified %q", reference)
	}
	return match, nil
}

type manifestObjectStorageBindingPlan struct {
	dependency gregalemanifest.BucketDependency
	bucket     api.ObjectBucket
	scope      string
	prefix     string
	permission string
	bindings   api.ObjectStorageComputeBindingList
}

// deployManifestObjectStorageBindings attaches existing ready buckets to the
// app and lets the API seal the provider-neutral S3 settings into the app
// environment. It intentionally does not provision buckets: resource creation
// stays explicit through `gregale add bucket`, which keeps CI deploys free of
// unexpected storage side effects.
func deployManifestObjectStorageBindings(ctx context.Context, client manifestObjectStorageClient, slug, cwd string, environments ...string) error {
	manifest, present, err := gregalemanifest.Load(cwd)
	if err != nil {
		return err
	}
	if !present || manifest == nil || len(manifest.Buckets) == 0 {
		return nil
	}
	if err := manifest.Validate(); err != nil {
		return err
	}

	environment := ""
	if len(environments) > 0 {
		environment = strings.TrimSpace(environments[0])
	}
	matching := make([]gregalemanifest.BucketDependency, 0, len(manifest.Buckets))
	for _, dependency := range manifest.Buckets {
		if dependency.App == "" || dependency.App == slug {
			matching = append(matching, dependency)
		}
	}
	if len(matching) == 0 {
		return nil
	}
	catalog, err := client.ListObjectBuckets(ctx, slug)
	if err != nil {
		return fmt.Errorf("list object-storage buckets for %q: %w", slug, err)
	}

	plans := make([]manifestObjectStorageBindingPlan, 0, len(matching))
	seen := make(map[string]struct{}, len(matching))
	for i, dependency := range matching {
		scope, err := manifestScopeForDeclared(dependency.Scope, environment)
		if err != nil {
			return fmt.Errorf("bucket dependency %d (%q): %w", i+1, dependency.Bucket, err)
		}
		bucket, err := resolveManifestObjectBucket(catalog.Items, dependency.Bucket, scope)
		if err != nil {
			return fmt.Errorf("bucket dependency %d (%q): %w; provision it with `gregale add bucket` and retry", i+1, dependency.Bucket, err)
		}
		if bucket.State != "ready" {
			return fmt.Errorf("bucket dependency %d (%q): bucket is %s; retry after it becomes ready", i+1, dependency.Bucket, bucket.State)
		}
		prefix := strings.TrimSpace(dependency.Prefix)
		if prefix == "" {
			prefix = defaultAddBucketBindingPrefix(bucket.Name)
		}
		key := strings.Join([]string{bucket.ID, scope, prefix}, "\x00")
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("bucket dependency %d (%q): duplicate resolved (bucket, scope, prefix) binding", i+1, dependency.Bucket)
		}
		seen[key] = struct{}{}
		bindings, err := client.ListObjectStorageComputeBindings(ctx, slug, bucket.ID)
		if err != nil {
			return fmt.Errorf("bucket dependency %d (%q): list compute bindings: %w", i+1, dependency.Bucket, err)
		}
		plans = append(plans, manifestObjectStorageBindingPlan{
			dependency: dependency,
			bucket:     bucket,
			scope:      scope,
			prefix:     prefix,
			permission: dependency.EffectivePermission(),
			bindings:   bindings,
		})
	}

	for i, plan := range plans {
		var existing *api.ObjectStorageComputeBinding
		for j := range plan.bindings.Items {
			binding := &plan.bindings.Items[j]
			if binding.Scope == plan.bucket.Scope && binding.Prefix == plan.prefix {
				existing = binding
				break
			}
		}
		if existing != nil {
			if existing.Credential.Status != "" && existing.Credential.Status != "active" {
				return fmt.Errorf("bucket dependency %d (%q): existing binding %q is %s", i+1, plan.dependency.Bucket, existing.ID, existing.Credential.Status)
			}
			if existing.Credential.Permission != "" && existing.Credential.Permission != plan.permission {
				return fmt.Errorf("bucket dependency %d (%q): existing binding %q has permission %q, manifest requests %q; delete the binding and redeploy", i+1, plan.dependency.Bucket, existing.ID, existing.Credential.Permission, plan.permission)
			}
			if !jsonOutput {
				_, _ = fmt.Fprintf(osStdout, "  ✓ %s: bucket %s attached as %s\n", slug, plan.bucket.Name, plan.prefix)
			}
			continue
		}

		bindingCtx := api.ContextWithIdempotencyKey(ctx, addResourceIdempotencyKey("manifest-bucket-binding", slug, plan.bucket.ID, plan.scope, plan.prefix))
		binding, err := client.CreateObjectStorageComputeBinding(bindingCtx, slug, plan.bucket.ID, api.CreateObjectStorageComputeBindingRequest{
			Label: strings.TrimSpace(plan.dependency.EffectiveLabel()), Permission: plan.permission, Prefix: plan.prefix,
		})
		if err != nil {
			return fmt.Errorf("bucket dependency %d (%q): create compute binding: %w", i+1, plan.dependency.Bucket, err)
		}
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStdout, "  ✓ %s: bucket %s attached as %s\n", slug, plan.bucket.Name, binding.Prefix)
		}
	}
	return nil
}

// manifestDeploymentScope combines resource declarations before the deploy
// request is built. A deployment has one environment scope; keeping database
// and bucket bindings on the same scope prevents secrets from being attached
// to a different workload environment than the code that consumes them.
func manifestDeploymentScope(slug, cwd string, environments ...string) (string, error) {
	manifest, present, err := gregalemanifest.Load(cwd)
	if err != nil {
		return "", err
	}
	if !present || manifest == nil {
		return "", nil
	}
	if err := manifest.Validate(); err != nil {
		return "", err
	}
	environment := ""
	if len(environments) > 0 {
		environment = strings.TrimSpace(environments[0])
	}
	var scope string
	addScope := func(kind, app, declared string) error {
		if app != "" && app != slug {
			return nil
		}
		current, scopeErr := manifestScopeForDeclared(declared, environment)
		if scopeErr != nil {
			return fmt.Errorf("%s dependency: %w", kind, scopeErr)
		}
		if scope != "" && scope != current {
			return fmt.Errorf("manifest dependencies for app %q use multiple scopes (%q and %q); use one scope per deployment", slug, scope, current)
		}
		scope = current
		return nil
	}
	for _, dependency := range manifest.Databases {
		if err := addScope("database", dependency.App, dependency.Scope); err != nil {
			return "", err
		}
	}
	for _, dependency := range manifest.Buckets {
		if err := addScope("bucket", dependency.App, dependency.Scope); err != nil {
			return "", err
		}
	}
	if environment != "" || scope == api.DefaultEnvScope {
		return "", nil
	}
	return scope, nil
}

func manifestScopeForDeclared(declared, environment string) (string, error) {
	environment = strings.TrimSpace(environment)
	scope := strings.TrimSpace(declared)
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	if environment == "" {
		return scope, nil
	}
	if declared != "" && scope != environment {
		return "", fmt.Errorf("scope %q does not match deployment environment %q", scope, environment)
	}
	return environment, nil
}
