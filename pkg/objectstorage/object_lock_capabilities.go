package objectstorage

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// Enabling this capability is an explicit operator declaration of the native
// contract. Event holds require a separate declaration for compatible backends.
// Capability changes never change immutable placement or cancel accepted work.
//
// CreationOnly declares a provider that accepts Object Lock only in the
// CreateBucket request and never on an existing bucket (OVHcloud, AWS before
// 2023). Customers then choose Object Lock when they create a bucket, and a
// later enablement is refused with ErrObjectLockRequiresNewBucket
// (ADR-20261119065000).
type ObjectLockConfig struct {
	Enabled      bool `json:"enabled,omitempty"`
	EventHolds   bool `json:"event_holds,omitempty"`
	CreationOnly bool `json:"creation_only,omitempty"`
}

// ErrObjectLockRequiresNewBucket refuses enabling Object Lock on an existing
// bucket of a creation-only backend: the provider cannot add it later.
var ErrObjectLockRequiresNewBucket = errors.New("object lock can only be enabled when the bucket is created")

// ObjectLockBucketCreator creates a provider bucket with Object Lock enabled
// in the creation request itself (ADR-20261119065000).
type ObjectLockBucketCreator interface {
	CreateObjectLockBucket(context.Context, string) error
}

func validateObjectLockConfig(c ObjectLockConfig, driver string) error {
	if (c.EventHolds || c.CreationOnly) && !c.Enabled || c.Enabled && driver != "s3" {
		return fmt.Errorf("object storage: Object Lock requires an enabled S3 backend")
	}
	return nil
}

// CreationCapable reports whether a new bucket on this backend can be created
// with Object Lock: the operator enabled bucket configuration and the provider
// can send the creation flag.
func (c ObjectLockConfig) CreationCapable(p Provider) bool {
	_, creator := p.(ObjectLockBucketCreator)
	return creator && c.PublicCapabilities(p).BucketConfiguration
}

// SupportsNativeObjectLock checks the full contract needed for protected,
// version-accounted writes, regardless of operator capability flags.
func SupportsNativeObjectLock(p Provider) bool {
	_, bucket := p.(BucketObjectLockProvider)
	_, version := p.(ObjectVersionLockProvider)
	_, versioning := p.(BucketVersioningProvider)
	_, inventory := p.(ObjectVersionInventoryProvider)
	_, history := p.(ObjectVersionLister)
	_, writes := p.(ObjectWriteProtectionProvider)
	_, deletion := p.(ObjectVersionDeleter)
	_, mutable := p.(MutableObjectDeleter)
	return bucket && version && versioning && inventory && history && writes && deletion && mutable
}

func (c ObjectLockConfig) PublicCapabilities(p Provider) api.ObjectLockCapabilities {
	enabled := c.Enabled && SupportsNativeObjectLock(p)
	return api.ObjectLockCapabilities{BucketConfiguration: enabled, DefaultEventHold: enabled && c.EventHolds, VersionEventHold: enabled && c.EventHolds, WriteEventHold: enabled && c.EventHolds, VersionRetention: enabled, VersionLegalHold: enabled}
}

func (c ObjectLockConfig) ValidateConfiguration(p Provider, v api.ObjectBucketObjectLockConfiguration) error {
	if !v.Enabled || !v.Valid() {
		return ErrInvalid
	}
	if !c.PublicCapabilities(p).BucketConfiguration || v.DefaultRetention != nil && v.DefaultRetention.DefaultEventHold != nil && !c.EventHolds {
		return ErrUnsupported
	}
	return nil
}
