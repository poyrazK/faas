package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

// Public lifecycle wire types used by the embedded Client's configuration and
// discovery methods. Completed discovery does not imply completed cleanup.
type (
	ObjectBucketLifecycleRequest        = api.ObjectBucketLifecycleRequest
	ObjectBucketLifecycle               = api.ObjectBucketLifecycle
	ObjectLifecycleRule                 = api.ObjectLifecycleRule
	ObjectLifecycleFilter               = api.ObjectLifecycleFilter
	ObjectLifecycleExpiration           = api.ObjectLifecycleExpiration
	ObjectLifecycleNoncurrentExpiration = api.ObjectLifecycleNoncurrentExpiration
	ObjectLifecycleScan                 = api.ObjectLifecycleScan
)
