package state

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// ObjectMultipartPartCopyIntent preserves private source identity and the exact
// requested range. It is evidence of intended IO, not proof that IO has drained.
// Empty and "null" native selectors remain distinct from immutable versions.
type ObjectMultipartPartCopyIntent struct {
	Schema                   int    `json:"schema"`
	SourceBucketID           string `json:"source_bucket_id"`
	SourceBackendID          string `json:"source_backend_id"`
	SourceBackendFingerprint string `json:"source_backend_fingerprint"`
	SourcePhysicalName       string `json:"source_physical_name"`
	SourceKey                string `json:"source_key"`
	SourceRequestedVersionID string `json:"source_requested_version_id"`
	SourceVersionID          string `json:"source_version_id"`
	SourceETag               string `json:"source_etag"`
	SourceSize               int64  `json:"source_size"`
	DestinationKey           string `json:"destination_key"`
	ProviderUploadID         string `json:"provider_upload_id"`
	HasRange                 bool   `json:"has_range"`
	RangeFirst               int64  `json:"range_first"`
	RangeLast                int64  `json:"range_last"`
	ExpectedSize             int64  `json:"expected_size"`
	IfMatch                  string `json:"if_match"`
	IfNoneMatch              string `json:"if_none_match"`
	IfModifiedSince          string `json:"if_modified_since"`
	IfUnmodifiedSince        string `json:"if_unmodified_since"`
}

type ObjectMultipartPartCopyMutationStore interface {
	DispatchObjectMultipartPartCopyMutation(context.Context, ObjectBucket, string, int32, string, ObjectMultipartPartCopyIntent) (ObjectBucketMutation, error)
	ReadObjectMultipartPartCopyIntent(context.Context, ObjectBucketMutation) (ObjectMultipartPartCopyIntent, error)
}

func validMultipartPartCopyIntent(i ObjectMultipartPartCopyIntent) bool {
	if i.Schema != 1 || !validObjectMutationToken(i.SourceBucketID) || i.SourceBackendID == "" || i.SourceBackendFingerprint == "" || i.SourcePhysicalName == "" || !validVersionReferenceText(i.SourceKey, api.MaxObjectS3ListTextBytes) || !validVersionReferenceText(i.DestinationKey, api.MaxObjectS3ListTextBytes) || i.ProviderUploadID == "" || !validObjectUploadETag(i.SourceETag) || i.SourceSize < 0 || i.SourceSize > api.MaxObjectUploadBytes || i.ExpectedSize < 1 || i.ExpectedSize > i.SourceSize || i.ExpectedSize > api.MaxObjectSinglePutBytes || i.SourceRequestedVersionID != "" && i.SourceRequestedVersionID != i.SourceVersionID || !validPartCopyIntentDate(i.IfModifiedSince) || !validPartCopyIntentDate(i.IfUnmodifiedSince) {
		return false
	}
	for _, s := range []string{i.SourceKey, i.DestinationKey, i.SourceVersionID, i.SourceRequestedVersionID, i.SourceETag, i.ProviderUploadID, i.IfMatch, i.IfNoneMatch, i.IfModifiedSince, i.IfUnmodifiedSince} {
		if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return false
		}
	}
	if i.HasRange {
		return i.RangeFirst >= 0 && i.RangeLast >= i.RangeFirst && i.RangeLast < i.SourceSize && i.ExpectedSize == i.RangeLast-i.RangeFirst+1
	}
	return i.RangeFirst == 0 && i.RangeLast == 0 && i.ExpectedSize == i.SourceSize
}

func validPartCopyIntentDate(s string) bool {
	if s == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	return err == nil && !t.IsZero() && t.Nanosecond() == 0
}
