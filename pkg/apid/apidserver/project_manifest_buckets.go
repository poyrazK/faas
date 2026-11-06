package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// resolvedManifestBucket is one gregale.yaml bucket dependency of a project
// deploy, resolved before any mutation (ADR-20261106105015 §1). Project
// deploys used to skip `buckets:` without a word; the CLI applied it only to
// single-app deploys.
type resolvedManifestBucket struct {
	appSlug string
	bucket  object.ManifestBucket
}

// resolveProjectManifestBuckets validates the root manifest's buckets for the
// selected workloads: the caller may bind storage, each entry names one app,
// and a bucket that is not created must already exist on an existing app.
func (s *server) resolveProjectManifestBuckets(r *http.Request, acct state.Account, dir string, workloads []reposcan.Workload, environment string) ([]resolvedManifestBucket, *api.Problem) {
	manifest, present, err := gregalemanifest.Load(dir)
	if err != nil || !present || manifest == nil || len(manifest.Buckets) == 0 {
		// An unreadable manifest is reported by loadAndResolveProjectManifest.
		return nil, nil
	}
	invalid := func(format string, a ...any) *api.Problem {
		return api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid, "Invalid manifest", fmt.Sprintf(format, a...))
	}
	// `app` may name the service or its allocated app slug, which differs
	// when the bare name was taken (ADR-20261022120205).
	selected := map[string]string{}
	var selectedSlugs []string
	for _, workload := range workloads {
		slug := strings.ToLower(reconcile.AppSlug(workload))
		selected[strings.ToLower(workload.Name)] = slug
		selected[slug] = slug
		selectedSlugs = append(selectedSlugs, slug)
	}
	sort.Strings(selectedSlugs)
	environment = strings.TrimSpace(environment)
	var out []resolvedManifestBucket
	for i, dependency := range manifest.Buckets {
		appSlug := strings.ToLower(strings.TrimSpace(dependency.App))
		if appSlug == "" {
			if len(selectedSlugs) != 1 {
				return nil, invalid("bucket[%d]: app is required when deploying multiple workloads", i)
			}
			appSlug = selectedSlugs[0]
		} else if slug, ok := selected[appSlug]; ok {
			appSlug = slug
		} else {
			continue // dormant until that workload is deployed, as for databases
		}
		scope := dependency.EffectiveScope()
		if environment != "" {
			if dependency.Scope != "" && scope != environment {
				return nil, api.NewProblem(http.StatusConflict, CodeAppManifestInvalid, "Manifest environment conflict",
					fmt.Sprintf("bucket[%d]: scope %q does not match deployment environment %q", i, scope, environment))
			}
			scope = environment
		}
		if dependency.Triggers != nil && len(*dependency.Triggers) > 0 && !api.MustLimitsFor(acct.Plan).AsyncInvokeAllowed {
			return nil, api.NewProblem(http.StatusForbidden, "plan_feature_unavailable", "Bucket triggers unavailable",
				fmt.Sprintf("bucket[%d]: the %s plan does not include asynchronous invocation", i, acct.Plan))
		}
		prefix := strings.TrimSpace(dependency.Prefix)
		if prefix == "" {
			prefix = api.DefaultObjectStorageBindingPrefix(dependency.Bucket)
		}
		out = append(out, resolvedManifestBucket{appSlug: appSlug, bucket: object.ManifestBucket{
			Name: strings.TrimSpace(dependency.Bucket), Scope: scope, Prefix: prefix,
			Permission: dependency.EffectivePermission(), Label: strings.TrimSpace(dependency.EffectiveLabel()),
			Create: dependency.Create != nil, Region: createRegion(dependency.Create), Triggers: dependency.Triggers,
		}})
	}
	if len(out) == 0 {
		return nil, nil
	}
	// Binding a bucket mints compute credentials, which takes storage:manage
	// (ADR-20261031162858 amendment 1), as on the single-app path.
	if !middleware.HasScope(r, api.ScopesStorageManageSurface...) {
		return nil, api.NewProblem(http.StatusForbidden, "insufficient_scope", "Insufficient scope",
			"gregale.yaml binds object-storage buckets, which needs a credential with storage:manage; a deploy token or a deploy:write key cannot bind them")
	}
	if !s.object.ObjectStorageEnabled() {
		return nil, api.NewProblem(http.StatusServiceUnavailable, "object_storage_unavailable", "Object storage unavailable",
			"gregale.yaml declares buckets, but object storage is not enabled on this installation")
	}
	// Buckets are app-owned and billed to their owner; organization projects
	// bind them explicitly for now, so refuse before anything is created
	// under the deploying member's account.
	if sharedComposePostgres(r.Context()) {
		return nil, manifestBucketProblem("gregale.yaml", object.ErrManifestBucketsOrganization)
	}
	// Per app, the checks `gregale add bucket` runs, before any mutation.
	byApp := map[string][]object.ManifestBucket{}
	var order []string
	for _, dependency := range out {
		if _, seen := byApp[dependency.appSlug]; !seen {
			order = append(order, dependency.appSlug)
		}
		byApp[dependency.appSlug] = append(byApp[dependency.appSlug], dependency.bucket)
	}
	apps := map[string]*state.App{}
	for _, slug := range order {
		app, err := s.store.AppBySlug(r.Context(), slug)
		switch {
		case errors.Is(err, state.ErrNotFound):
			apps[slug] = nil // created by this deploy
		case err != nil:
			return nil, api.ErrCapacity("could not load bucket owners")
		default:
			apps[slug] = &app
		}
		if err := s.object.CheckManifestBuckets(r.Context(), acct, apps[slug], byApp[slug]); err != nil {
			return nil, manifestBucketProblem(slug, err)
		}
	}
	for i, dependency := range out {
		if dependency.bucket.Create {
			continue
		}
		app := apps[dependency.appSlug]
		if app == nil {
			return nil, invalid("bucket %q: %s does not exist yet, so the bucket cannot either; add `create: true` to create it on deploy", dependency.bucket.Name, dependency.appSlug)
		}
		bucket, exists, err := s.object.FindManifestBucket(r.Context(), acct, app.ID, dependency.bucket.Name, dependency.bucket.Scope)
		if err != nil {
			return nil, api.ErrCapacity("could not list buckets")
		}
		if !exists {
			return nil, api.NewProblem(http.StatusNotFound, "object_storage_not_found", "Bucket not found",
				fmt.Sprintf("bucket[%d]: %s has no bucket %q in scope %q; add `create: true` to create it on deploy", i, dependency.appSlug, dependency.bucket.Name, dependency.bucket.Scope))
		}
		if bucket.State != "ready" {
			return nil, api.NewProblem(http.StatusConflict, "object_storage_conflict", "Bucket not ready",
				fmt.Sprintf("bucket %q is %s; retry after it becomes ready", bucket.Name, bucket.State))
		}
	}
	return out, nil
}

func createRegion(create *gregalemanifest.BucketCreate) string {
	if create == nil {
		return ""
	}
	return strings.TrimSpace(create.Region)
}

// manifestBucketPlan is the plan view of the buckets gregale.yaml creates or
// binds. Provision.Database carries the bucket name and each binding's
// EnvKey the S3 settings prefix.
func manifestBucketPlan(buckets []resolvedManifestBucket) []api.PlanManaged {
	out := make([]api.PlanManaged, 0, len(buckets))
	for _, dependency := range buckets {
		out = append(out, api.PlanManaged{Name: dependency.bucket.Name, Kind: "bucket", Source: "gregale.yaml",
			Provision: &api.PlanManagedProvision{Database: dependency.bucket.Name, Existing: !dependency.bucket.Create,
				Bindings: []api.PlanManagedBinding{{Workload: dependency.appSlug, EnvKey: dependency.bucket.Prefix}}}})
	}
	return out
}

// applyProjectManifestBuckets creates and binds the resolved buckets after
// the project's apps exist and before their builds.
func (s *server) applyProjectManifestBuckets(ctx context.Context, acct state.Account, buckets []resolvedManifestBucket, apps []state.App) *api.Problem {
	if len(buckets) == 0 {
		return nil
	}
	bySlug := make(map[string]state.App, len(apps))
	for _, app := range apps {
		bySlug[strings.ToLower(app.Slug)] = app
	}
	grouped := map[string][]object.ManifestBucket{}
	var order []string
	for _, dependency := range buckets {
		if _, seen := grouped[dependency.appSlug]; !seen {
			order = append(order, dependency.appSlug)
		}
		grouped[dependency.appSlug] = append(grouped[dependency.appSlug], dependency.bucket)
	}
	for _, slug := range order {
		app, ok := bySlug[slug]
		if !ok {
			return api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid, "Invalid manifest",
				fmt.Sprintf("bucket dependency targets workload %q, but no deployed app exists", slug))
		}
		prob, err := s.object.EnsureManifestBuckets(ctx, acct, app, grouped[slug])
		if prob != nil {
			return prob
		}
		if err != nil {
			return manifestBucketProblem(slug, err)
		}
	}
	return nil
}

func manifestBucketProblem(slug string, err error) *api.Problem {
	var limit *state.ObjectStorageLimitError
	switch {
	case errors.Is(err, object.ErrManifestBucketsOrganization):
		return api.NewProblem(http.StatusConflict, "object_storage_organization_manifest", "Bucket declarations unavailable", err.Error())
	case errors.As(err, &limit):
		problem := api.NewProblem(http.StatusConflict, "object_storage_conflict", "Bucket limit reached",
			fmt.Sprintf("%s: gregale.yaml would give it %d buckets; the limit is %d (%s)", slug, limit.Observed, limit.Limit, limit.Kind))
		problem.Limit, problem.Observed, problem.DocsURL = &limit.Limit, &limit.Observed, api.ObjectStorageLimitsDocsURL
		return problem
	case errors.Is(err, object.ErrObjectStorageDisabled):
		return api.NewProblem(http.StatusServiceUnavailable, "object_storage_unavailable", "Object storage unavailable",
			"gregale.yaml declares buckets, but object storage is not enabled on this installation")
	case errors.Is(err, object.ErrObjectStorageProviderRefused):
		return api.NewProblem(http.StatusServiceUnavailable, "object_storage_unavailable", "Object storage unavailable",
			"the storage provider is refusing the platform's credentials; no bucket was created")
	case errors.Is(err, state.ErrObjectUsageStale):
		return api.NewProblem(http.StatusServiceUnavailable, "object_storage_usage_stale", "Object storage unavailable",
			"storage accounting is stale, so new buckets are paused; retry later")
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, "object_storage_not_found", "Bucket not found", fmt.Sprintf("%s: %v", slug, err))
	case errors.Is(err, state.ErrConflict), errors.Is(err, objectstorage.ErrConflict):
		return api.NewProblem(http.StatusConflict, "object_storage_conflict", "Bucket conflict",
			fmt.Sprintf("%s: %v", slug, err))
	case errors.Is(err, objectstorage.ErrInvalid):
		return api.NewProblem(http.StatusBadRequest, "object_storage_invalid", "Invalid bucket", fmt.Sprintf("%s: %v", slug, err))
	}
	return api.NewProblem(http.StatusServiceUnavailable, "object_storage_unavailable", "Object storage unavailable",
		fmt.Sprintf("%s: buckets from gregale.yaml could not be created or bound; retry the deploy", slug))
}
