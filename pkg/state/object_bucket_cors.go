package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// ObjectBucketCORSStore holds a customer's CORS rules for a bucket
// (ADR-20261025202805). apid is the only writer.
type ObjectBucketCORSStore interface {
	// GetObjectBucketCORS returns the bucket's rules, or an empty
	// configuration at revision 0 when none are set.
	GetObjectBucketCORS(ctx context.Context, account, app, bucket string) (api.ObjectBucketCORS, error)
	// PutObjectBucketCORS replaces the rules and advances the revision.
	PutObjectBucketCORS(ctx context.Context, account, app, bucket string, rules []api.ObjectBucketCORSRule) (api.ObjectBucketCORS, error)
	// DeleteObjectBucketCORS removes the rules; removing none succeeds.
	DeleteObjectBucketCORS(ctx context.Context, account, app, bucket string) error
}

// ObjectBucketCORSReader serves the branded gateway, which has resolved the
// bucket from a credential or upload grant. No rules is a nil slice.
type ObjectBucketCORSReader interface {
	ObjectBucketCORSRules(ctx context.Context, bucket string) ([]api.ObjectBucketCORSRule, error)
}

func cloneObjectBucketCORSRules(rules []api.ObjectBucketCORSRule) []api.ObjectBucketCORSRule {
	out := make([]api.ObjectBucketCORSRule, len(rules))
	for i, r := range rules {
		r.AllowedOrigins = append([]string(nil), r.AllowedOrigins...)
		r.AllowedMethods = append([]string(nil), r.AllowedMethods...)
		r.AllowedHeaders = append([]string(nil), r.AllowedHeaders...)
		r.ExposeHeaders = append([]string(nil), r.ExposeHeaders...)
		out[i] = r
	}
	return out
}
