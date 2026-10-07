package state

import (
	"context"
	"encoding/hex"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
)

// PUT intent pins admitted bytes and any authenticated declared SHA-256 before
// IO. BodySHA256 records only a completely read, integrity-validated body. It
// proves neither provider acceptance nor that an uncertain request has drained.
type ObjectMultipartPartPutIntent struct {
	Schema           int    `json:"schema"`
	DestinationKey   string `json:"destination_key"`
	ProviderUploadID string `json:"provider_upload_id"`
	ExpectedSize     int64  `json:"expected_size"`
	ExpectedSHA256   string `json:"expected_sha256"`
	BodySHA256       string `json:"-"`
}

type ObjectMultipartPartPutMutationStore interface {
	DispatchObjectMultipartPartPutMutation(context.Context, ObjectBucket, string, int32, string, ObjectMultipartPartPutIntent) (ObjectBucketMutation, error)
	ObserveObjectMultipartPartBody(context.Context, ObjectBucketMutation, string) error
	ReadObjectMultipartPartPutIntent(context.Context, ObjectBucketMutation) (ObjectMultipartPartPutIntent, error)
}

func validMultipartPartSHA256(s string) bool {
	if len(s) != 64 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func validMultipartPartPutIntent(i ObjectMultipartPartPutIntent) bool {
	return i.Schema == 1 && validVersionReferenceText(i.DestinationKey, api.MaxObjectS3ListTextBytes) && i.ProviderUploadID != "" && !strings.ContainsRune(i.ProviderUploadID, 0) && i.ExpectedSize >= 1 && i.ExpectedSize <= api.MaxObjectSinglePutBytes && (i.ExpectedSHA256 == "" || validMultipartPartSHA256(i.ExpectedSHA256)) && (i.BodySHA256 == "" || validMultipartPartSHA256(i.BodySHA256) && (i.ExpectedSHA256 == "" || i.ExpectedSHA256 == i.BodySHA256))
}
func validMultipartPartReceipt(r ObjectBucketMutation) bool {
	return validObjectMutationBucket(r.Bucket) && validObjectMutationToken(r.ID) && r.ID == r.MultipartPartWriterID && r.Kind == ObjectBucketMutationRequest && r.UploadID == "" && r.MultipartUploadID == ""
}
