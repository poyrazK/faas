package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

// The bucket catalog advertises the configured transfer contract.
type (
	ObjectBucket        = api.ObjectBucket
	ObjectBucketList    = api.ObjectBucketList
	ObjectSignRequest   = api.ObjectSignRequest
	ObjectSignedRequest = api.ObjectSignedRequest
	ObjectEncryption    = api.ObjectEncryption
)
