package objectstorage

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ VersionReadPresigner = (*S3)(nil)
var _ ObjectVersionLister = (*S3)(nil)

func (p *S3) PresignVersionRead(ctx context.Context, bucket, method, key, version string, checksum bool, expires int64) (SignedRequest, error) {
	if !validNativeVersionID(version) || (SignRequest{Method: method, Key: key, ExpiresIn: expires}).Validate(api.MaxObjectSinglePutBytes) != nil || method != http.MethodGet && method != http.MethodHead {
		return SignedRequest{}, ErrInvalid
	}
	ttl := time.Duration(expires) * time.Second
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	options := func(o *s3.PresignOptions) { o.Expires = ttl }
	mode := types.ChecksumMode("")
	if checksum {
		mode = types.ChecksumModeEnabled
	}
	result := SignedRequest{Method: method, Headers: map[string]string{}, ExpiresAt: time.Now().UTC().Add(ttl)}
	var headers http.Header
	if method == http.MethodGet {
		out, err := p.signer.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(version), ChecksumMode: mode}, options)
		if err != nil {
			return SignedRequest{}, ErrUnavailable
		}
		result.URL, headers = out.URL, out.SignedHeader
	} else {
		out, err := p.signer.PresignHeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(version), ChecksumMode: mode}, options)
		if err != nil {
			return SignedRequest{}, ErrUnavailable
		}
		result.URL, headers = out.URL, out.SignedHeader
	}
	for name, values := range headers {
		if !strings.EqualFold(name, "Host") {
			result.Headers[name] = strings.Join(values, ",")
		}
	}
	return result, nil
}

func (p *S3) ListObjectVersionPage(ctx context.Context, bucket string, r ObjectVersionListRequest) (ObjectVersionListPage, error) {
	page := ObjectVersionListPage{}
	if r.Limit < 1 || r.Limit > api.MaxObjectS3ListItems || !validVersionListText(r.Prefix) || !validVersionListText(r.KeyMarker) || !validVersionDelimiter(r.Delimiter) || r.ProviderVersionMarker != "" && (r.KeyMarker == "" || !validNativeVersionID(r.ProviderVersionMarker)) {
		return page, ErrInvalid
	}
	in := &s3.ListObjectVersionsInput{Bucket: aws.String(bucket), Prefix: stringPtrOrNil(r.Prefix), Delimiter: stringPtrOrNil(r.Delimiter), KeyMarker: stringPtrOrNil(r.KeyMarker), VersionIdMarker: stringPtrOrNil(r.ProviderVersionMarker), MaxKeys: aws.Int32(r.Limit), EncodingType: types.EncodingTypeUrl}
	out, err := p.client.ListObjectVersions(ctx, in, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return page, normalizeVersionHistoryError(err)
	}
	if out == nil || out.IsTruncated == nil || len(out.Versions)+len(out.DeleteMarkers)+len(out.CommonPrefixes) > int(r.Limit) {
		return page, ErrUnavailable
	}
	seen := map[string]bool{}
	appendVersion := func(key, id, etag string, size int64, modified *time.Time, latest *bool, marker bool) error {
		key, e := historyResponseKey(key, out.EncodingType)
		if e != nil || !strings.HasPrefix(key, r.Prefix) || !validNativeVersionID(id) || modified == nil || modified.IsZero() || latest == nil || size < 0 || size > api.MaxObjectUploadBytes || !marker && !validUploadETag(etag) || seen[key+"\x00"+id] {
			return ErrUnavailable
		}
		seen[key+"\x00"+id] = true
		page.Items = append(page.Items, ListedObjectVersion{Object: Object{Key: key, ETag: etag, Size: size, LastModified: *modified}, ProviderVersionID: id, IsLatest: *latest, DeleteMarker: marker})
		return nil
	}
	for _, v := range out.Versions {
		if v.Size == nil {
			return ObjectVersionListPage{}, ErrUnavailable
		}
		class := string(v.StorageClass)
		if class == "" {
			class = string(types.StorageClassStandard)
		}
		if !slices.Contains(types.StorageClassStandard.Values(), types.StorageClass(class)) {
			return ObjectVersionListPage{}, ErrUnavailable
		}
		if err = appendVersion(aws.ToString(v.Key), aws.ToString(v.VersionId), aws.ToString(v.ETag), *v.Size, v.LastModified, v.IsLatest, false); err != nil {
			return ObjectVersionListPage{}, err
		}
		page.Items[len(page.Items)-1].StorageClass = class
	}
	for _, v := range out.DeleteMarkers {
		if err = appendVersion(aws.ToString(v.Key), aws.ToString(v.VersionId), "", 0, v.LastModified, v.IsLatest, true); err != nil {
			return ObjectVersionListPage{}, err
		}
	}
	for _, v := range out.CommonPrefixes {
		prefix, e := historyResponseKey(aws.ToString(v.Prefix), out.EncodingType)
		if e != nil || r.Delimiter == "" || !strings.HasPrefix(prefix, r.Prefix) || !strings.HasSuffix(prefix, r.Delimiter) || seen["prefix\x00"+prefix] {
			return ObjectVersionListPage{}, ErrUnavailable
		}
		seen["prefix\x00"+prefix] = true
		page.CommonPrefixes = append(page.CommonPrefixes, prefix)
	}
	if aws.ToBool(out.IsTruncated) {
		key, e := historyResponseKey(aws.ToString(out.NextKeyMarker), out.EncodingType)
		id := aws.ToString(out.NextVersionIdMarker)
		if e != nil || !strings.HasPrefix(key, r.Prefix) || id != "" && !validNativeVersionID(id) || key == r.KeyMarker && id == r.ProviderVersionMarker || len(page.Items)+len(page.CommonPrefixes) == 0 {
			return ObjectVersionListPage{}, ErrUnavailable
		}
		page.NextKeyMarker, page.NextProviderVersionMarker = key, id
	} else if aws.ToString(out.NextKeyMarker) != "" || aws.ToString(out.NextVersionIdMarker) != "" {
		return ObjectVersionListPage{}, ErrUnavailable
	}
	return page, nil
}

func validVersionListText(s string) bool {
	if len(s) > api.MaxObjectS3ListTextBytes || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func validVersionDelimiter(s string) bool {
	return validVersionListText(s) && len(s) <= api.MaxObjectS3DelimiterBytes && (s == "" || utf8.RuneCountInString(s) == 1)
}
