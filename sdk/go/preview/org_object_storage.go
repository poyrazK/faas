package preview

import "github.com/poyrazK/faas/sdk/go/internal/api"

type (
	CreateObjectBucketRequest                = api.CreateObjectBucketRequest
	BucketObject                             = api.BucketObject
	BucketObjectPage                         = api.BucketObjectPage
	ObjectBucketAccessGrant                  = api.ObjectBucketAccessGrant
	ObjectBucketAccessGrantList              = api.ObjectBucketAccessGrantList
	SetObjectBucketAccessGrantRequest        = api.SetObjectBucketAccessGrantRequest
	ObjectS3Credential                       = api.ObjectS3Credential
	ObjectS3CredentialList                   = api.ObjectS3CredentialList
	ObjectS3CredentialSecret                 = api.ObjectS3CredentialSecret
	CreateObjectS3CredentialRequest          = api.CreateObjectS3CredentialRequest
	ObjectStorageComputeBinding              = api.ObjectStorageComputeBinding
	ObjectStorageComputeBindingList          = api.ObjectStorageComputeBindingList
	CreateObjectStorageComputeBindingRequest = api.CreateObjectStorageComputeBindingRequest
	ObjectStorageComputeBindingSecretKeys    = api.ObjectStorageComputeBindingSecretKeys
)
