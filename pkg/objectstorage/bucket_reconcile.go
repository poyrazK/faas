package objectstorage

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"google.golang.org/api/iterator"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// PhysicalBucketPrefix starts every physical bucket name Gregale creates
// ("gregale-" + the bucket ID without dashes). Reconciliation lists only
// provider buckets with this prefix.
const PhysicalBucketPrefix = "gregale-"

// BucketLister is the optional provider capability operator reconciliation
// uses after a control-plane database restore: it lists the physical buckets
// in the backend's dedicated namespace whose names start with prefix, sorted
// (ADR-20261025111015).
type BucketLister interface {
	ListBucketNames(ctx context.Context, prefix string) ([]string, error)
}

var _ BucketLister = (*S3)(nil)
var _ BucketLister = (*GCS)(nil)

func (p *S3) ListBucketNames(ctx context.Context, prefix string) ([]string, error) {
	var names []string
	pager := s3.NewListBucketsPaginator(p.client, &s3.ListBucketsInput{Prefix: aws.String(prefix), MaxBuckets: aws.Int32(1000)})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, normalize(err)
		}
		for _, b := range page.Buckets {
			// Some S3-compatible providers ignore the prefix parameter.
			if name := aws.ToString(b.Name); strings.HasPrefix(name, prefix) {
				names = append(names, name)
			}
		}
		if len(names) > api.MaxObjectStorageReconcileBuckets {
			return nil, ErrUnavailable
		}
	}
	sort.Strings(names)
	return names, nil
}

// gcsBucketLister is implemented by the real GCS store; test stores need not.
type gcsBucketLister interface {
	ListBucketNames(ctx context.Context, project, prefix string) ([]string, error)
}

func (p *GCS) ListBucketNames(ctx context.Context, prefix string) ([]string, error) {
	lister, ok := p.store.(gcsBucketLister)
	if !ok || p.projectID == "" {
		return nil, ErrUnsupported
	}
	return lister.ListBucketNames(ctx, p.projectID, prefix)
}

func (s *googleGCSStore) ListBucketNames(ctx context.Context, project, prefix string) ([]string, error) {
	it := s.client.Buckets(ctx, project)
	it.Prefix = prefix
	var names []string
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, normalizeGCS(err)
		}
		if strings.HasPrefix(attrs.Name, prefix) {
			names = append(names, attrs.Name)
		}
		if len(names) > api.MaxObjectStorageReconcileBuckets {
			return nil, ErrUnavailable
		}
	}
	sort.Strings(names)
	return names, nil
}

// Bucket reconciliation findings.
const (
	// ReconcileProviderOnly is a physical bucket with no catalog row: created
	// after the restore point, or leaked. It holds unbilled, unmetered data.
	ReconcileProviderOnly = "provider_only"
	// ReconcileCatalogMissing is a live catalog bucket whose physical bucket
	// is gone: typically deleted after the restore point.
	ReconcileCatalogMissing = "catalog_missing"
	// ReconcileDeletedPresent is a deleted catalog bucket whose physical
	// bucket still exists: a cleanup that did not finish.
	ReconcileDeletedPresent = "deleted_present"
)

// ReconcileFinding is one disagreement between a backend and the catalog.
type ReconcileFinding struct {
	Kind         string `json:"kind"`
	BackendID    string `json:"backend_id"`
	PhysicalName string `json:"physical_name"`
	BucketID     string `json:"bucket_id,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
	State        string `json:"state,omitempty"`
}

// ReconcileBuckets compares a backend's physical Gregale buckets with the
// catalog rows bound to it. Buckets still being created or that failed to
// create are not expected to exist yet, so they are never reported missing.
// Findings are sorted by physical name.
func ReconcileBuckets(backendID string, physical []string, catalog []state.ObjectBucket) []ReconcileFinding {
	present := make(map[string]bool, len(physical))
	for _, name := range physical {
		present[name] = true
	}
	known := make(map[string]bool, len(catalog))
	var findings []ReconcileFinding
	for _, b := range catalog {
		if b.PhysicalName == "" {
			continue
		}
		known[b.PhysicalName] = true
		finding := ReconcileFinding{BackendID: backendID, PhysicalName: b.PhysicalName, BucketID: b.ID, AccountID: b.AccountID, State: b.State}
		switch {
		case b.State == "deleted" && present[b.PhysicalName]:
			finding.Kind = ReconcileDeletedPresent
		case (b.State == "ready" || b.State == "deleting") && !present[b.PhysicalName]:
			finding.Kind = ReconcileCatalogMissing
		default:
			continue
		}
		findings = append(findings, finding)
	}
	for _, name := range physical {
		if !known[name] {
			findings = append(findings, ReconcileFinding{Kind: ReconcileProviderOnly, BackendID: backendID, PhysicalName: name})
		}
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].PhysicalName < findings[j].PhysicalName })
	return findings
}
