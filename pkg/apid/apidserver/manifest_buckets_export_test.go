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
