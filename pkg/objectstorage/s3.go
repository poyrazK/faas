package objectstorage

import (
	"context"
	"crypto/md5" // #nosec G501 -- Required S3 wire integrity checksum, not authentication.
	"encoding/base64"
	"errors"
	"io"
	"net/http"
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
	if limit < 1 || limit > 1000 {
		return ObjectPage{}, ErrInvalid
	}
	in := &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(prefix), MaxKeys: aws.Int32(limit)}
	if cursor != "" {
		in.ContinuationToken = aws.String(cursor)
	}
	out, err := p.client.ListObjectsV2(ctx, in)
	if err != nil {
		return ObjectPage{}, normalize(err)
	}
	page := ObjectPage{Items: make([]Object, 0, len(out.Contents))}
	for _, o := range out.Contents {
		page.Items = append(page.Items, Object{Key: aws.ToString(o.Key), Size: aws.ToInt64(o.Size), LastModified: aws.ToTime(o.LastModified)})
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
	if r.Method == http.MethodPut {
		contentType := r.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		in := &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(r.Key), ContentLength: r.SizeBytes, ContentType: aws.String(contentType)}
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
	} else {
		out, err := p.signer.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(r.Key), ResponseContentDisposition: aws.String("attachment"), ResponseContentType: aws.String("application/octet-stream")}, options)
		if err != nil {
			return SignedRequest{}, ErrUnavailable
		}
		result.URL = out.URL
	}
	return result, nil
}

const multipartSessionMetadata = "gregale-upload-id"

func (p *S3) EnsureMultipartUpload(ctx context.Context, bucket string, r MultipartCreateRequest) (string, error) {
	if r.SessionID == "" || len(r.SessionID) > 128 || !ValidKey(r.Key) || r.SizeBytes <= 0 || r.SizeBytes > api.MaxObjectUploadBytes || ValidateContentType(r.ContentType) != nil {
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

func (p *S3) CompleteMultipartUpload(ctx context.Context, bucket string, r MultipartCompleteRequest) error {
	if r.SessionID == "" || !ValidKey(r.Key) || r.ProviderUploadID == "" || r.SizeBytes <= 0 || len(r.Parts) < 1 || len(r.Parts) > 10000 {
		return ErrInvalid
	}
	parts := make([]types.CompletedPart, 0, len(r.Parts))
	for i, part := range r.Parts {
		if part.PartNumber != int32(i+1) || part.ETag == "" {
			return ErrInvalid
		}
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
