package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/state"
)

// ApplyProjectManifestBucketsForTest runs a project apply's gregale.yaml
// bucket step for one app (ADR-20261106105015 §1).
func (s *server) ApplyProjectManifestBucketsForTest(ctx context.Context, acct state.Account, app state.App, buckets []object.ManifestBucket) *api.Problem {
	resolved := make([]resolvedManifestBucket, 0, len(buckets))
	for _, bucket := range buckets {
		resolved = append(resolved, resolvedManifestBucket{appSlug: app.Slug, bucket: bucket})
	}
	return s.applyProjectManifestBuckets(ctx, acct, resolved, []state.App{app})
}

// CheckManifestBucketsForTest runs the plan-time bucket checks.
func (s *server) CheckManifestBucketsForTest(ctx context.Context, acct state.Account, app *state.App, buckets []object.ManifestBucket) error {
	return s.object.CheckManifestBuckets(ctx, acct, app, buckets)
}

// ObjectStorageMaxBucketsPerAppForTest is the registry's per-app bucket limit.
func (s *server) ObjectStorageMaxBucketsPerAppForTest() int {
	return s.object.Host.ObjectStorage().MaxBucketsPerApp
}
