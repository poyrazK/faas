package objectstorage

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// Enabling this capability is an explicit operator declaration of the native
// contract. Event holds require a separate declaration for compatible backends.
// Capability changes never change immutable placement or cancel accepted work.
type ObjectLockConfig struct {
	Enabled    bool `json:"enabled,omitempty"`
	EventHolds bool `json:"event_holds,omitempty"`
}

func validateObjectLockConfig(c ObjectLockConfig, driver string) error {
	if c.EventHolds && !c.Enabled || c.Enabled && driver != "s3" {
		return fmt.Errorf("object storage: Object Lock requires an enabled S3 backend")
	}
	return nil
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
