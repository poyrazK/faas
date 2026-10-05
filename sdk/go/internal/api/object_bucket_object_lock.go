package api

import "time"

// DesiredConfiguration is owned intent; ObservedConfiguration is native truth.
// EnabledRequired is permanent even if the provider later returns an unknown
// policy. Unknown observations never supply an empty/default-clear selection.
type ObjectBucketObjectLock struct {
	BucketID              string                               `json:"bucket_id"`
	State                 string                               `json:"state"`
	Revision              int64                                `json:"revision"`
	EnabledRequired       bool                                 `json:"enabled_required"`
	ObservedKnown         bool                                 `json:"observed_known"`
	ObservedConfiguration *ObjectBucketObjectLockConfiguration `json:"observed_configuration,omitempty"`
	DesiredConfiguration  *ObjectBucketObjectLockConfiguration `json:"desired_configuration,omitempty"`
	LastErrorCode         string                               `json:"last_error_code,omitempty"`
	UpdatedAt             time.Time                            `json:"updated_at"`
}

func (c ObjectBucketObjectLockConfiguration) Equal(other ObjectBucketObjectLockConfiguration) bool {
	if c.Enabled != other.Enabled || (c.DefaultRetention == nil) != (other.DefaultRetention == nil) {
		return false
	}
	if c.DefaultRetention == nil {
		return true
	}
	a, b := c.DefaultRetention, other.DefaultRetention
	if a.Mode != b.Mode || !equalObjectLockInteger(a.Days, b.Days) || !equalObjectLockInteger(a.Years, b.Years) || (a.DefaultEventHold == nil) != (b.DefaultEventHold == nil) {
		return false
	}
	return a.DefaultEventHold == nil || equalObjectLockInteger(a.DefaultEventHold.Days, b.DefaultEventHold.Days) && equalObjectLockInteger(a.DefaultEventHold.Years, b.DefaultEventHold.Years)
}

func equalObjectLockInteger(a, b *int32) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// Object Lock is permanently enabled. An omitted default_retention clears
// defaults for future versions without changing existing version protection.
type ObjectBucketObjectLockRequest struct {
	Configuration ObjectBucketObjectLockConfiguration `json:"configuration"`
}

// These fields describe the customer bucket configuration surface. Native
// per-version primitives alone do not advertise version management support.
type ObjectLockCapabilities struct {
	BucketConfiguration bool `json:"bucket_configuration"`
	DefaultEventHold    bool `json:"default_event_hold"`
	VersionRetention    bool `json:"version_retention"`
	VersionLegalHold    bool `json:"version_legal_hold"`
}
