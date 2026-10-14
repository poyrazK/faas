package objectstorage

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
)

func ValidateObjectChecksums(c ObjectChecksums, parts bool) bool {
	if c.Type != "" && c.Type != objectchecksum.FullObject && c.Type != objectchecksum.Composite {
		return false
	}
	for algorithm, value := range map[string]string{"CRC32": c.CRC32, "CRC32C": c.CRC32C, "CRC64NVME": c.CRC64NVME, "SHA1": c.SHA1, "SHA256": c.SHA256} {
		if value == "" {
			continue
		}
		if !parts && c.Type == objectchecksum.Composite {
			n := ChecksumPartCount(value)
			if algorithm == "CRC64NVME" || n < 1 || n > api.MaxMultipartParts || !objectchecksum.ValidPart(algorithm, strings.TrimSuffix(value, "-"+strconv.Itoa(n))) {
				return false
			}
		} else if !objectchecksum.ValidPart(algorithm, value) {
			return false
		}
	}
	return true
}

func (p *S3) GetObjectAttributes(ctx context.Context, bucket string, r ObjectAttributesRequest) (ObjectAttributes, error) {
	var result ObjectAttributes
	if !ValidObjectAttributesRequest(r) {
		return result, ErrInvalid
	}
	attrs := make([]types.ObjectAttributes, len(r.Attributes))
	seen := map[string]bool{}
	for i, a := range r.Attributes {
		if seen[a] {
			return result, ErrInvalid
		}
		seen[a] = true
		switch a {
		case "ETag", "Checksum", "ObjectParts", "StorageClass", "ObjectSize":
		default:
			return result, ErrInvalid
		}
		attrs[i] = types.ObjectAttributes(a)
	}
	in := &s3.GetObjectAttributesInput{Bucket: aws.String(bucket), Key: aws.String(r.Key), ObjectAttributes: attrs, MaxParts: aws.Int32(r.MaxParts), PartNumberMarker: aws.String(strconv.FormatInt(int64(r.PartNumberMarker), 10))}
	if r.ProviderVersionID != "" {
		in.VersionId = aws.String(r.ProviderVersionID)
	}
	out, err := p.client.GetObjectAttributes(ctx, in, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return result, objectAttributesError(err, r.ProviderVersionID)
	}
	if out == nil || aws.ToBool(out.DeleteMarker) || !validCopySnapshotVersion(out.ResultMetadata, aws.ToString(out.VersionId), r.ProviderVersionID) {
		return result, ErrUnavailable
	}
	result.ProviderVersionID = aws.ToString(out.VersionId)
	result.LastModified = aws.ToTime(out.LastModified)
	if seen["ETag"] {
		result.ETag = aws.ToString(out.ETag)
		if !validUploadETag(result.ETag) {
			return ObjectAttributes{}, ErrUnavailable
		}
	}
	if seen["ObjectSize"] {
		if out.ObjectSize == nil || *out.ObjectSize < 0 || *out.ObjectSize > api.MaxObjectUploadBytes {
			return ObjectAttributes{}, ErrUnavailable
		}
		result.Size = out.ObjectSize
	}
	if seen["StorageClass"] {
		result.StorageClass = string(out.StorageClass)
		if len(result.StorageClass) > api.MaxObjectStorageClassBytes || strings.ContainsAny(result.StorageClass, "\x00\r\n") {
			return ObjectAttributes{}, ErrUnavailable
		}
	}
	if seen["Checksum"] && out.Checksum != nil {
		c := out.Checksum
		result.Checksums = &ObjectChecksums{CRC32: aws.ToString(c.ChecksumCRC32), CRC32C: aws.ToString(c.ChecksumCRC32C), CRC64NVME: aws.ToString(c.ChecksumCRC64NVME), SHA1: aws.ToString(c.ChecksumSHA1), SHA256: aws.ToString(c.ChecksumSHA256), Type: string(c.ChecksumType)}
		if !ValidateObjectChecksums(*result.Checksums, false) {
			return ObjectAttributes{}, ErrUnavailable
		}
	}
	if seen["ObjectParts"] && out.ObjectParts != nil {
		x := out.ObjectParts
		marker, e1 := strconv.ParseInt(aws.ToString(x.PartNumberMarker), 10, 32)
		if x.PartNumberMarker == nil {
			marker = 0
			e1 = nil
		}
		next, e2 := strconv.ParseInt(aws.ToString(x.NextPartNumberMarker), 10, 32)
		if x.NextPartNumberMarker == nil {
			next = 0
			e2 = nil
		}
		if e1 != nil || e2 != nil || marker != int64(r.PartNumberMarker) || x.IsTruncated == nil || x.TotalPartsCount == nil || *x.TotalPartsCount < 1 || *x.TotalPartsCount > api.MaxMultipartParts || x.MaxParts == nil || *x.MaxParts < 1 || *x.MaxParts > r.MaxParts || len(x.Parts) > int(*x.MaxParts) || len(x.Parts) > int(*x.TotalPartsCount) {
			return ObjectAttributes{}, ErrUnavailable
		}
		page := &ObjectAttributeParts{Count: *x.TotalPartsCount, Marker: int32(marker), NextMarker: int32(next), MaxParts: *x.MaxParts, Truncated: *x.IsTruncated}
		last := r.PartNumberMarker
		for _, item := range x.Parts {
			part := ObjectAttributePart{Number: aws.ToInt32(item.PartNumber), Size: aws.ToInt64(item.Size), Checksums: ObjectChecksums{CRC32: aws.ToString(item.ChecksumCRC32), CRC32C: aws.ToString(item.ChecksumCRC32C), CRC64NVME: aws.ToString(item.ChecksumCRC64NVME), SHA1: aws.ToString(item.ChecksumSHA1), SHA256: aws.ToString(item.ChecksumSHA256)}}
			if item.Size == nil || part.Size < 1 || part.Size > api.MaxObjectSinglePutBytes || part.Number <= last || part.Number > api.MaxMultipartParts || !ValidateObjectChecksums(part.Checksums, true) {
				return ObjectAttributes{}, ErrUnavailable
			}
			last = part.Number
			page.Parts = append(page.Parts, part)
		}
		if page.Truncated && (len(page.Parts) == 0 || page.NextMarker != last || last >= api.MaxMultipartParts) || !page.Truncated && page.NextMarker != 0 {
			return ObjectAttributes{}, ErrUnavailable
		}
		result.Parts = page
	}
	return result, nil
}

func objectAttributesError(err error, expected string) error {
	var response *smithyhttp.ResponseError
	if !errors.As(err, &response) || response.Response == nil || response.Response.Response == nil {
		return normalize(err)
	}
	h := response.Response.Header
	if h.Get("X-Amz-Delete-Marker") == "" {
		return normalize(err)
	}
	version := h.Get("X-Amz-Version-Id")
	status := http.StatusNotFound
	if expected != "" {
		status = http.StatusMethodNotAllowed
	}
	if len(h.Values("X-Amz-Delete-Marker")) != 1 || h.Get("X-Amz-Delete-Marker") != "true" || len(h.Values("X-Amz-Version-Id")) != 1 || !validNativeVersionID(version) || expected != "" && version != expected || response.HTTPStatusCode() != status {
		return ErrUnavailable
	}
	marker := &ObjectAttributesDeleteMarker{ProviderVersionID: version}
	if value := h.Get("Last-Modified"); value != "" {
		var parseErr error
		marker.LastModified, parseErr = http.ParseTime(value)
		if len(h.Values("Last-Modified")) != 1 || parseErr != nil {
			return ErrUnavailable
		}
	}
	return marker
}

func ValidObjectAttributesRequest(r ObjectAttributesRequest) bool {
	if !ValidKey(r.Key) || len(r.Attributes) < 1 || len(r.Attributes) > api.MaxObjectAttributesSelections || r.MaxParts < 1 || r.MaxParts > api.MaxObjectS3ListItems || r.PartNumberMarker < 0 || r.PartNumberMarker > api.MaxMultipartParts || r.ProviderVersionID != "" && !validNativeVersionID(r.ProviderVersionID) {
		return false
	}
	seen := map[string]bool{}
	for _, a := range r.Attributes {
		if seen[a] {
			return false
		}
		seen[a] = true
		switch a {
		case "ETag", "Checksum", "ObjectParts", "StorageClass", "ObjectSize":
		default:
			return false
		}
	}
	return true
}
