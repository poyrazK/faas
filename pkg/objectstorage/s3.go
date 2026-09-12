package objectstorage

import (
	"context"
	"crypto/md5" // #nosec G501 -- Required S3 wire integrity checksum, not authentication.
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/onebox-faas/faas/pkg/api"
)

type S3 struct {
	client  *s3.Client
	signer  *s3.PresignClient
	region  string
	origins []string
}

func NewS3(c BackendConfig, getenv func(string) string) (Provider, error) {
	key, secret := getenv(c.AccessKeyEnv), getenv(c.SecretKeyEnv)
	if key == "" || secret == "" {
		return nil, errors.New("S3 credential environment variables are missing")
	}
	client := s3.New(s3.Options{
		Region:                     c.S3Region,
		BaseEndpoint:               aws.String(c.Endpoint),
		UsePathStyle:               c.PathStyle,
		Credentials:                credentials.NewStaticCredentialsProvider(key, secret, getenv(c.SessionTokenEnv)),
		HTTPClient:                 &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		RetryMaxAttempts:           2,
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &S3{client: client, signer: s3.NewPresignClient(client), region: c.S3Region, origins: c.AllowedOrigins}, nil
}

func (p *S3) CreateBucket(ctx context.Context, bucket string) error {
	in := &s3.CreateBucketInput{Bucket: aws.String(bucket)}
	// AWS requires omission in us-east-1; R2 signs with auto but does not
	// use it as a placement constraint.
	if p.region != "us-east-1" && p.region != "auto" {
		in.CreateBucketConfiguration = &types.CreateBucketConfiguration{LocationConstraint: types.BucketLocationConstraint(p.region)}
	}
	_, err := p.client.CreateBucket(ctx, in)
	var apiErr smithy.APIError
	if err != nil && (!errors.As(err, &apiErr) || apiErr.ErrorCode() != "BucketAlreadyOwnedByYou") {
		return normalize(err)
	}
	// No ACL is sent: the S3 default is private, and R2 does not implement
	// canned ACLs. The dedicated operator identity must not expose buckets.
	if len(p.origins) > 0 {
		_, err = p.client.PutBucketCors(ctx, &s3.PutBucketCorsInput{Bucket: aws.String(bucket), CORSConfiguration: &types.CORSConfiguration{CORSRules: []types.CORSRule{{AllowedOrigins: p.origins, AllowedMethods: []string{"GET", "HEAD", "PUT"}, AllowedHeaders: []string{"content-type", "content-length", "content-md5"}, ExposeHeaders: []string{"ETag"}, MaxAgeSeconds: aws.Int32(3600)}}}}, func(o *s3.Options) { o.APIOptions = append(o.APIOptions, corsMD5Checksum) })
	}
	return normalize(err)
}

// Modern AWS SDKs send a mandatory CRC32 for PutBucketCors even with
// WhenRequired. Use the standard Content-MD5 form accepted by both AWS and
// compatible services (R2 does not implement that CRC32 CORS extension).
// This is scoped to CORS: object PUT signatures/checksums are unaffected.
func corsMD5Checksum(stack *middleware.Stack) error {
	if _, err := stack.Finalize.Remove("AWSChecksum:ComputeInputPayloadChecksum"); err != nil {
		return err
	}
	return stack.Build.Add(middleware.BuildMiddlewareFunc("GregaleCORSContentMD5", func(ctx context.Context, in middleware.BuildInput, next middleware.BuildHandler) (middleware.BuildOutput, middleware.Metadata, error) {
		req, ok := in.Request.(*smithyhttp.Request)
		if !ok {
			return middleware.BuildOutput{}, middleware.Metadata{}, ErrUnavailable
		}
		h := md5.New() // #nosec G401 -- Standard S3 Content-MD5 protocol checksum.
		if _, err := io.Copy(h, req.GetStream()); err != nil {
			return middleware.BuildOutput{}, middleware.Metadata{}, err
		}
		if err := req.RewindStream(); err != nil {
			return middleware.BuildOutput{}, middleware.Metadata{}, err
		}
		req.Header.Set("Content-MD5", base64.StdEncoding.EncodeToString(h.Sum(nil)))
		return next.HandleBuild(ctx, in)
	}), middleware.After)
}

func (p *S3) DeleteBucket(ctx context.Context, bucket string) error {
	_, err := p.client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
	if errors.Is(normalize(err), ErrNotFound) {
		return nil
	}
	return normalize(err)
}

func (p *S3) ListObjects(ctx context.Context, bucket, prefix, cursor string, limit int32) (ObjectPage, error) {
	return p.ListObjectsDelimited(ctx, bucket, prefix, "", cursor, limit)
}

func (p *S3) ListObjectsDelimited(ctx context.Context, bucket, prefix, delimiter, cursor string, limit int32) (ObjectPage, error) {
	if limit < 1 || limit > 1000 {
		return ObjectPage{}, ErrInvalid
	}
	in := &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(prefix), Delimiter: aws.String(delimiter), MaxKeys: aws.Int32(limit)}
	if cursor != "" {
		in.ContinuationToken = aws.String(cursor)
	}
	out, err := p.client.ListObjectsV2(ctx, in)
	if err != nil {
		return ObjectPage{}, normalize(err)
	}
	page := ObjectPage{Items: make([]Object, 0, len(out.Contents)), CommonPrefixes: make([]string, 0, len(out.CommonPrefixes))}
	for _, o := range out.Contents {
		page.Items = append(page.Items, Object{Key: aws.ToString(o.Key), Size: aws.ToInt64(o.Size), LastModified: aws.ToTime(o.LastModified)})
	}
	for _, prefix := range out.CommonPrefixes {
		if value := aws.ToString(prefix.Prefix); value != "" {
			page.CommonPrefixes = append(page.CommonPrefixes, value)
		}
	}
	if aws.ToBool(out.IsTruncated) {
		page.NextCursor = aws.ToString(out.NextContinuationToken)
	}
	return page, nil
}

func (p *S3) DeleteObject(ctx context.Context, bucket, key string) error {
	_, err := p.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	return normalize(err)
}

func (p *S3) CopyObject(ctx context.Context, bucket string, r CopyObjectRequest) (CopyObjectResult, error) {
	if !ValidKey(r.SourceKey) || !ValidKey(r.DestinationKey) {
		return CopyObjectResult{}, ErrInvalid
	}
	if r.MetadataDirective == "" {
		r.MetadataDirective = "COPY"
	}
	if r.MetadataDirective != "COPY" && r.MetadataDirective != "REPLACE" {
		return CopyObjectResult{}, ErrInvalid
	}
	if r.TaggingDirective == "" {
		r.TaggingDirective = "COPY"
	}
	if r.TaggingDirective != "COPY" && r.TaggingDirective != "REPLACE" {
		return CopyObjectResult{}, ErrInvalid
	}
	if r.TaggingDirective == "COPY" && len(r.Metadata.Tags) != 0 {
		return CopyObjectResult{}, ErrInvalid
	}
	if err := ValidateObjectMetadata(r.Metadata); err != nil {
		return CopyObjectResult{}, err
	}
	tagging, err := EncodeObjectTags(r.Metadata.Tags)
	if err != nil {
		return CopyObjectResult{}, err
	}
	in := &s3.CopyObjectInput{
		Bucket:             aws.String(bucket),
		CopySource:         aws.String(url.PathEscape(bucket + "/" + r.SourceKey)),
		Key:                aws.String(r.DestinationKey),
		Metadata:           r.Metadata.Metadata,
		ContentType:        stringPtrOrNil(r.Metadata.ContentType),
		CacheControl:       stringPtrOrNil(r.Metadata.CacheControl),
		ContentDisposition: stringPtrOrNil(r.Metadata.ContentDisposition),
		ContentEncoding:    stringPtrOrNil(r.Metadata.ContentEncoding),
		ContentLanguage:    stringPtrOrNil(r.Metadata.ContentLanguage),
		MetadataDirective:  types.MetadataDirective(r.MetadataDirective),
		TaggingDirective:   types.TaggingDirective(r.TaggingDirective),
	}
	if r.TaggingDirective == "REPLACE" {
		in.Tagging = aws.String(tagging)
	}
	out, err := p.client.CopyObject(ctx, in)
	if err != nil {
		return CopyObjectResult{}, normalize(err)
	}
	if out == nil || out.CopyObjectResult == nil || aws.ToString(out.CopyObjectResult.ETag) == "" {
		return CopyObjectResult{}, ErrUnavailable
	}
	return CopyObjectResult{ETag: aws.ToString(out.CopyObjectResult.ETag), LastModified: aws.ToTime(out.CopyObjectResult.LastModified)}, nil
}

func stringPtrOrNil(value string) *string {
	if value == "" {
		return nil
	}
	return aws.String(value)
}

// ReadObject is intentionally not part of the customer-facing Provider
// interface. It is used only by the operator-owned OVH access-log collector.
func (p *S3) ReadObject(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	out, err := p.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return nil, normalize(err)
	}
	return out.Body, nil
}

// WriteObject is the server-side streaming path used by authenticated upload
// routes. The SDK receives the caller's reader directly; no request-sized
// buffer is created here.
func (p *S3) WriteObject(ctx context.Context, bucket, key string, body io.Reader, size int64, metadata ObjectMetadata) (UploadResult, error) {
	if !ValidKey(key) || size < 0 || size > api.MaxObjectSinglePutBytes {
		return UploadResult{}, ErrInvalid
	}
	if err := ValidateObjectMetadata(metadata); err != nil {
		return UploadResult{}, err
	}
	tagging, err := EncodeObjectTags(metadata.Tags)
	if err != nil {
		return UploadResult{}, err
	}
	in := &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: body, ContentLength: aws.Int64(size),
		ContentType: stringPtrOrNil(metadata.ContentType), ContentEncoding: stringPtrOrNil(metadata.ContentEncoding),
		ContentLanguage: stringPtrOrNil(metadata.ContentLanguage), CacheControl: stringPtrOrNil(metadata.CacheControl),
		ContentDisposition: stringPtrOrNil(metadata.ContentDisposition), Metadata: metadata.Metadata,
	}
	if tagging != "" {
		in.Tagging = aws.String(tagging)
	}
	out, err := p.client.PutObject(ctx, in)
	if err != nil {
		return UploadResult{}, normalize(err)
	}
	if out == nil {
		return UploadResult{}, ErrUnavailable
	}
	return UploadResult{ETag: aws.ToString(out.ETag)}, nil
}

func (p *S3) ObjectSize(ctx context.Context, bucket, key string) (int64, error) {
	if !ValidKey(key) {
		return 0, ErrInvalid
	}
	out, err := p.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return 0, normalize(err)
	}
	if aws.ToInt64(out.ContentLength) < 0 {
		return 0, ErrUnavailable
	}
	return aws.ToInt64(out.ContentLength), nil
}

func (p *S3) Presign(ctx context.Context, bucket string, r SignRequest) (SignedRequest, error) {
	if err := r.Validate(api.MaxObjectSinglePutBytes); err != nil {
		return SignedRequest{}, err
	}
	ttl := time.Duration(r.ExpiresIn) * time.Second
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	options := func(o *s3.PresignOptions) { o.Expires = ttl }
	result := SignedRequest{Method: r.Method, Headers: map[string]string{}, ExpiresAt: time.Now().UTC().Add(ttl)}
	switch r.Method {
	case http.MethodPut:
		contentType := r.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		tagging, err := EncodeObjectTags(r.Tags)
		if err != nil {
			return SignedRequest{}, err
		}
		in := &s3.PutObjectInput{
			Bucket: aws.String(bucket), Key: aws.String(r.Key), ContentLength: r.SizeBytes, ContentType: aws.String(contentType),
			CacheControl: stringPtrOrNil(r.CacheControl), ContentDisposition: stringPtrOrNil(r.ContentDisposition),
			ContentEncoding: stringPtrOrNil(r.ContentEncoding), ContentLanguage: stringPtrOrNil(r.ContentLanguage),
			Metadata: r.Metadata,
		}
		if tagging != "" {
			in.Tagging = aws.String(tagging)
		}
		// The SDK does not sign Content-Length: 0. Binding the standard S3
		// empty-body digest prevents using that URL for a nonempty upload.
		if *r.SizeBytes == 0 {
			in.ContentMD5 = aws.String("1B2M2Y8AsgTpgAmY7PhCfg==")
		}
		out, err := p.signer.PresignPutObject(ctx, in, options)
		if err != nil {
			return SignedRequest{}, ErrUnavailable
		}
		// Refuse to issue a URL if the SDK drops the upload length binding.
		if (*r.SizeBytes > 0 && out.SignedHeader.Get("Content-Length") == "") || (*r.SizeBytes == 0 && out.SignedHeader.Get("Content-Md5") != "1B2M2Y8AsgTpgAmY7PhCfg==") {
			return SignedRequest{}, ErrUnavailable
		}
		result.URL = out.URL
		for name, values := range out.SignedHeader {
			if !strings.EqualFold(name, "Host") {
				result.Headers[name] = strings.Join(values, ",")
			}
		}
		result.Headers["Content-Type"] = contentType
	case http.MethodGet:
		out, err := p.signer.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(r.Key), ResponseContentDisposition: aws.String("attachment"), ResponseContentType: aws.String("application/octet-stream")}, options)
		if err != nil {
			return SignedRequest{}, ErrUnavailable
		}
		result.URL = out.URL
	default:
		out, err := p.signer.PresignHeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(r.Key)}, options)
		if err != nil {
			return SignedRequest{}, ErrUnavailable
		}
		result.URL = out.URL
	}
	return result, nil
}

func (p *S3) GetObjectTags(ctx context.Context, bucket, key string) (map[string]string, error) {
	if !ValidKey(key) {
		return nil, ErrInvalid
	}
	out, err := p.client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return nil, normalize(err)
	}
	tags := make(map[string]string, len(out.TagSet))
	for _, tag := range out.TagSet {
		key, value := aws.ToString(tag.Key), aws.ToString(tag.Value)
		tags[key] = value
	}
	if err := ValidateObjectMetadata(ObjectMetadata{Tags: tags}); err != nil {
		return nil, ErrUnavailable
	}
	return tags, nil
}

func (p *S3) PutObjectTags(ctx context.Context, bucket, key string, tags map[string]string) error {
	if !ValidKey(key) {
		return ErrInvalid
	}
	if err := ValidateObjectMetadata(ObjectMetadata{Tags: tags}); err != nil {
		return err
	}
	tagSet := make([]types.Tag, 0, len(tags))
	for key, value := range tags {
		tagSet = append(tagSet, types.Tag{Key: aws.String(key), Value: aws.String(value)})
	}
	_, err := p.client.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{Bucket: aws.String(bucket), Key: aws.String(key), Tagging: &types.Tagging{TagSet: tagSet}})
	return normalize(err)
}

func (p *S3) DeleteObjectTags(ctx context.Context, bucket, key string) error {
	if !ValidKey(key) {
		return ErrInvalid
	}
	_, err := p.client.DeleteObjectTagging(ctx, &s3.DeleteObjectTaggingInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	return normalize(err)
}

const multipartSessionMetadata = "gregale-upload-id"

func (p *S3) EnsureMultipartUpload(ctx context.Context, bucket string, r MultipartCreateRequest) (string, error) {
	if r.SessionID == "" || len(r.SessionID) > 128 || !ValidKey(r.Key) || r.SizeBytes < 0 || r.SizeBytes > api.MaxObjectUploadBytes || ValidateContentType(r.ContentType) != nil {
		return "", ErrInvalid
	}
	// A Gregale bucket does not expose native provider credentials. Combined
	// with the catalog's one-live-session-per-key rule, an exact-key upload is
	// the recovery identity when an initiate response is lost.
	var found string
	var keyMarker, uploadMarker *string
	for page := 0; page < 100; page++ {
		out, err := p.client.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{
			Bucket: aws.String(bucket), Prefix: aws.String(r.Key), MaxUploads: aws.Int32(1000),
			KeyMarker: keyMarker, UploadIdMarker: uploadMarker,
		})
		if err != nil {
			return "", normalize(err)
		}
		for _, upload := range out.Uploads {
			if aws.ToString(upload.Key) != r.Key {
				continue
			}
			id := aws.ToString(upload.UploadId)
			if id == "" || found != "" && found != id {
				return "", ErrConflict
			}
			found = id
		}
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		keyMarker, uploadMarker = out.NextKeyMarker, out.NextUploadIdMarker
		if keyMarker == nil || aws.ToString(keyMarker) == "" {
			return "", ErrUnavailable
		}
		if page == 99 {
			return "", ErrUnavailable
		}
	}
	if found != "" {
		return found, nil
	}
	contentType := r.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	out, err := p.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(r.Key), ContentType: aws.String(contentType),
		Metadata: map[string]string{multipartSessionMetadata: r.SessionID},
	})
	if err != nil {
		return "", normalize(err)
	}
	id := aws.ToString(out.UploadId)
	if id == "" {
		return "", ErrUnavailable
	}
	return id, nil
}

func (p *S3) PresignMultipartPart(ctx context.Context, bucket string, r MultipartPartRequest) (SignedRequest, error) {
	if !ValidKey(r.Key) || r.ProviderUploadID == "" || r.PartNumber < 1 || r.PartNumber > 10000 || r.SizeBytes < 1 || r.SizeBytes > api.MaxObjectSinglePutBytes || r.ExpiresIn < 0 || r.ExpiresIn > 900 {
		return SignedRequest{}, ErrInvalid
	}
	ttl := time.Duration(r.ExpiresIn) * time.Second
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	out, err := p.signer.PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket: aws.String(bucket), Key: aws.String(r.Key), UploadId: aws.String(r.ProviderUploadID),
		PartNumber: aws.Int32(r.PartNumber), ContentLength: aws.Int64(r.SizeBytes),
	}, func(o *s3.PresignOptions) { o.Expires = ttl })
	if err != nil || out.SignedHeader.Get("Content-Length") == "" {
		return SignedRequest{}, ErrUnavailable
	}
	result := SignedRequest{URL: out.URL, Method: http.MethodPut, Headers: map[string]string{}, ExpiresAt: time.Now().UTC().Add(ttl)}
	for name, values := range out.SignedHeader {
		if !strings.EqualFold(name, "Host") {
			result.Headers[name] = strings.Join(values, ",")
		}
	}
	return result, nil
}

func (p *S3) ListMultipartParts(ctx context.Context, bucket string, r MultipartListPartsRequest) (MultipartPartsPage, error) {
	if !ValidKey(r.Key) || r.ProviderUploadID == "" || r.PartNumberMarker < 0 || r.PartNumberMarker > 10000 || r.Limit < 1 || r.Limit > 1000 {
		return MultipartPartsPage{}, ErrInvalid
	}
	out, err := p.client.ListParts(ctx, &s3.ListPartsInput{
		Bucket: aws.String(bucket), Key: aws.String(r.Key), UploadId: aws.String(r.ProviderUploadID),
		PartNumberMarker: aws.String(strconv.FormatInt(int64(r.PartNumberMarker), 10)), MaxParts: aws.Int32(r.Limit),
	})
	if err != nil {
		return MultipartPartsPage{}, normalize(err)
	}
	page := MultipartPartsPage{Items: make([]MultipartPart, 0, len(out.Parts))}
	for _, part := range out.Parts {
		partNumber := aws.ToInt32(part.PartNumber)
		etag := aws.ToString(part.ETag)
		size := aws.ToInt64(part.Size)
		if partNumber < 1 || partNumber > 10000 || etag == "" || len(etag) > 256 || size < 1 || size > api.MaxObjectSinglePutBytes {
			return MultipartPartsPage{}, ErrUnavailable
		}
		page.Items = append(page.Items, MultipartPart{
			PartNumber: partNumber, ETag: etag, SizeBytes: size, LastModified: aws.ToTime(part.LastModified),
		})
	}
	if aws.ToBool(out.IsTruncated) {
		next, parseErr := strconv.ParseInt(aws.ToString(out.NextPartNumberMarker), 10, 32)
		if parseErr != nil || next < 1 || next > 10000 {
			return MultipartPartsPage{}, ErrUnavailable
		}
		page.NextPartNumberMarker = int32(next)
		if page.NextPartNumberMarker <= r.PartNumberMarker {
			return MultipartPartsPage{}, ErrUnavailable
		}
	}
	return page, nil
}

func (p *S3) CompleteMultipartUpload(ctx context.Context, bucket string, r MultipartCompleteRequest) error {
	if r.SessionID == "" || !ValidKey(r.Key) || r.ProviderUploadID == "" || r.SizeBytes <= 0 || len(r.Parts) < 1 || len(r.Parts) > 10000 {
		return ErrInvalid
	}
	parts := make([]types.CompletedPart, 0, len(r.Parts))
	var previousPart int32
	for _, part := range r.Parts {
		if part.PartNumber < 1 || part.PartNumber > 10000 || part.PartNumber <= previousPart || part.ETag == "" {
			return ErrInvalid
		}
		previousPart = part.PartNumber
		parts = append(parts, types.CompletedPart{PartNumber: aws.Int32(part.PartNumber), ETag: aws.String(part.ETag)})
	}
	_, err := p.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(r.Key), UploadId: aws.String(r.ProviderUploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	})
	if err == nil {
		return nil
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "NoSuchUpload" {
		return normalize(err)
	}
	// CompleteMultipartUpload can succeed upstream while its response is lost.
	// Only operator-created session metadata plus exact length proves that this
	// session, rather than a later overwrite, completed.
	head, headErr := p.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(r.Key)})
	if headErr != nil {
		return normalize(headErr)
	}
	if aws.ToInt64(head.ContentLength) != r.SizeBytes || head.Metadata[multipartSessionMetadata] != r.SessionID {
		return ErrConflict
	}
	return nil
}

func (p *S3) AbortMultipartUpload(ctx context.Context, bucket string, r MultipartAbortRequest) error {
	if !ValidKey(r.Key) || r.ProviderUploadID == "" {
		return ErrInvalid
	}
	_, err := p.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(r.Key), UploadId: aws.String(r.ProviderUploadID),
	})
	if errors.Is(normalize(err), ErrNotFound) {
		return nil
	}
	return normalize(err)
}

func normalize(err error) error {
	if err == nil {
		return nil
	}
	var e smithy.APIError
	if errors.As(err, &e) {
		switch e.ErrorCode() {
		case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch", "ExpiredToken", "InvalidToken", "AuthorizationHeaderMalformed":
			return ErrConfiguration
		case "NoSuchBucket", "NoSuchKey", "NoSuchUpload", "NotFound":
			return ErrNotFound
		case "BucketNotEmpty":
			return ErrNotEmpty
		case "BucketAlreadyExists", "OperationAborted":
			return ErrConflict
		case "InvalidArgument", "InvalidRequest", "InvalidPart", "InvalidPartOrder", "EntityTooSmall", "EntityTooLarge":
			return ErrInvalid
		}
	}
	return ErrUnavailable
}
