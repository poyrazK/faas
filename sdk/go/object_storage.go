package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

// The bucket catalog advertises the configured transfer contract.
type (
	ObjectWriteReceipt                   = api.ObjectWriteReceipt
	ObjectWriteReceiptList               = api.ObjectWriteReceiptList
	ObjectUploadRoute                    = api.ObjectUploadRoute
	ObjectUploadRouteList                = api.ObjectUploadRouteList
	CreateObjectUploadRouteRequest       = api.CreateObjectUploadRouteRequest
	ObjectBucket                         = api.ObjectBucket
	ObjectBucketList                     = api.ObjectBucketList
	ObjectSignRequest                    = api.ObjectSignRequest
	ObjectSignedRequest                  = api.ObjectSignedRequest
	ObjectEncryption                     = api.ObjectEncryption
	ObjectMultipartUpload                = api.ObjectMultipartUpload
	ObjectMultipartUploadList            = api.ObjectMultipartUploadList
	CreateObjectMultipartUploadRequest   = api.CreateObjectMultipartUploadRequest
	ObjectMultipartPartSignRequest       = api.ObjectMultipartPartSignRequest
	ObjectMultipartCompletedPart         = api.ObjectMultipartCompletedPart
	ObjectMultipartPart                  = api.ObjectMultipartPart
	ObjectMultipartPartList              = api.ObjectMultipartPartList
	CompleteObjectMultipartUploadRequest = api.CompleteObjectMultipartUploadRequest
)
