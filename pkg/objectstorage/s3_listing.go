package objectstorage

import (
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/onebox-faas/faas/pkg/api"
)

// Listings feed capacity reconciliation and verified cleanup. Missing fields
// must not turn an incomplete provider response into proof of zero usage.
func objectListingPage(out *s3.ListObjectsV2Output, r ObjectListRequest) (ObjectPage, error) {
	if out == nil || out.IsTruncated == nil || len(out.Contents)+len(out.CommonPrefixes) > int(r.Limit) {
		return ObjectPage{}, ErrUnavailable
	}
	page := ObjectPage{Items: make([]Object, 0, len(out.Contents)), CommonPrefixes: make([]string, 0, len(out.CommonPrefixes))}
	last := ""
	seen := map[string]bool{}
	for _, o := range out.Contents {
		key, err := historyResponseKey(aws.ToString(o.Key), out.EncodingType)
		if err != nil || !strings.HasPrefix(key, r.Prefix) || key <= r.StartAfter || last != "" && key <= last || o.Size == nil || *o.Size < 0 || *o.Size > api.MaxObjectUploadBytes {
			return ObjectPage{}, ErrUnavailable
		}
		last, seen[key] = key, true
		page.Items = append(page.Items, Object{Key: key, ETag: aws.ToString(o.ETag), Size: *o.Size, LastModified: aws.ToTime(o.LastModified)})
	}
	last = ""
	for _, v := range out.CommonPrefixes {
		prefix, err := historyResponseKey(aws.ToString(v.Prefix), out.EncodingType)
		if err != nil || r.Delimiter == "" || !strings.HasPrefix(prefix, r.Prefix) || !strings.HasSuffix(prefix, r.Delimiter) || prefix <= r.StartAfter || last != "" && prefix <= last || seen[prefix] {
			return ObjectPage{}, ErrUnavailable
		}
		last, seen[prefix] = prefix, true
		page.CommonPrefixes = append(page.CommonPrefixes, prefix)
	}
	if aws.ToBool(out.IsTruncated) {
		next := aws.ToString(out.NextContinuationToken)
		if next == "" || next == r.Cursor || len(next) > api.MaxObjectS3ListCursorBytes || len(page.Items)+len(page.CommonPrefixes) == 0 {
			return ObjectPage{}, ErrUnavailable
		}
		page.NextCursor = next // Tokens are opaque; only keys are URL-decoded.
	}
	return page, nil
}

func multipartPartsPage(out *s3.ListPartsOutput, bucket string, r MultipartListPartsRequest) (MultipartPartsPage, error) {
	if out == nil || out.IsTruncated == nil || len(out.Parts) > int(r.Limit) || out.Bucket != nil && *out.Bucket != bucket || out.Key != nil && *out.Key != r.Key || out.UploadId != nil && *out.UploadId != r.ProviderUploadID {
		return MultipartPartsPage{}, ErrUnavailable
	}
	page := MultipartPartsPage{Items: make([]MultipartPart, 0, len(out.Parts))}
	last := r.PartNumberMarker
	for _, part := range out.Parts {
		number, etag, size := aws.ToInt32(part.PartNumber), aws.ToString(part.ETag), aws.ToInt64(part.Size)
		if number <= last || number > api.MaxMultipartParts || !validUploadETag(etag) || size < 1 || size > api.MaxObjectSinglePutBytes {
			return MultipartPartsPage{}, ErrUnavailable
		}
		last = number
		page.Items = append(page.Items, MultipartPart{PartNumber: number, ETag: etag, SizeBytes: size, LastModified: aws.ToTime(part.LastModified)})
	}
	if aws.ToBool(out.IsTruncated) {
		next, err := strconv.ParseInt(aws.ToString(out.NextPartNumberMarker), 10, 32)
		if err != nil || len(page.Items) == 0 || next != int64(last) {
			return MultipartPartsPage{}, ErrUnavailable
		}
		page.NextPartNumberMarker = last
	}
	return page, nil
}
