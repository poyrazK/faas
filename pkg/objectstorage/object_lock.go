package objectstorage

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// These optional interfaces use private physical placement and immutable
// native selectors. Ownership and irreversible enablement belong to the
// durable service; implementing the primitive does not advertise capability.
// On an unsupported configuration read, Enabled may still be true and must be
// persisted as a sticky versioning fence before returning the error.
type BucketObjectLockProvider interface {
	GetBucketObjectLock(context.Context, string) (api.ObjectBucketObjectLockConfiguration, error)
	PutBucketObjectLock(context.Context, string, api.ObjectBucketObjectLockConfiguration) error
}

// An exact nonempty version is mandatory. A qualified permanently versioned
// Object Lock bucket may also protect its existing null version. Never retry
// mutations against a changing current-object selector.
type ObjectVersionLockProvider interface {
	GetObjectVersionRetention(context.Context, string, string, string) (api.ObjectVersionRetention, error)
	PutObjectVersionRetention(context.Context, string, string, string, api.ObjectVersionRetention, bool) error
	GetObjectVersionLegalHold(context.Context, string, string, string) (api.ObjectVersionLegalHold, error)
	PutObjectVersionLegalHold(context.Context, string, string, string, api.ObjectVersionLegalHold) error
}
