package s3gateway

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/objectoverlay"
	"github.com/onebox-faas/faas/pkg/objectstorage"
)

// asOfVersion returns the version of key current at at, and false when the
// key held no data then (objectoverlay.AsOfVersion). countRequest is called
// before each provider request and stops the lookup when it returns false.
func asOfVersion(ctx context.Context, lister objectstorage.ObjectVersionLister, bucket, key string, at time.Time, countRequest func() bool) (objectstorage.ListedObjectVersion, bool, error) {
	return objectoverlay.AsOfVersion(ctx, lister, bucket, key, at, overlayCounter(countRequest))
}
