package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

type ObjectRetentionPeriod = api.ObjectRetentionPeriod
type ObjectLockDefaultRetention = api.ObjectLockDefaultRetention
type ObjectBucketObjectLockConfiguration = api.ObjectBucketObjectLockConfiguration
type ObjectBucketObjectLock = api.ObjectBucketObjectLock
type ObjectBucketObjectLockRequest = api.ObjectBucketObjectLockRequest
type ObjectLockCapabilities = api.ObjectLockCapabilities

type ObjectVersionRetention = api.ObjectVersionRetention
type ObjectVersionLegalHold = api.ObjectVersionLegalHold
type ObjectVersionProtection = api.ObjectVersionProtection
type ObjectVersionRetentionRequest = api.ObjectVersionRetentionRequest
type ObjectVersionLegalHoldRequest = api.ObjectVersionLegalHoldRequest
type ObjectVersionRetentionResult = api.ObjectVersionRetentionResult
type ObjectVersionLegalHoldResult = api.ObjectVersionLegalHoldResult

// ObjectWriteProtection selects fixed protection on a newly created version.
type ObjectWriteProtection = api.ObjectWriteProtection
