package objectstorage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

var _ ConditionalStateProvider = (*S3)(nil)

func (p *S3) ReadStateObject(ctx context.Context, bucket, key string, maxBytes int64) ([]byte, string, error) {
	if !ValidKey(key) || bucket == "" || maxBytes <= 0 || maxBytes == int64(^uint64(0)>>1) {
		return nil, "", ErrInvalid
	}
	out, err := p.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return nil, "", normalize(err)
	}
	if out == nil || out.Body == nil {
		return nil, "", ErrUnavailable
	}
	defer func() { _ = out.Body.Close() }()
	if !validUploadETag(aws.ToString(out.ETag)) || out.ContentLength == nil || *out.ContentLength < 0 || *out.ContentLength > maxBytes || aws.ToBool(out.DeleteMarker) {
		return nil, "", ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(out.Body, maxBytes+1))
	if err != nil || int64(len(body)) > maxBytes || int64(len(body)) != *out.ContentLength {
		return nil, "", ErrUnavailable
	}
	return body, aws.ToString(out.ETag), nil
}

func (p *S3) WriteStateObject(ctx context.Context, bucket, key string, body []byte, expectedETag string) (string, error) {
	if !ValidKey(key) || bucket == "" || expectedETag != "" && !validUploadETag(expectedETag) {
		return "", ErrInvalid
	}
	in := &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(body), ContentLength: aws.Int64(int64(len(body))), ContentType: aws.String("application/json"), CacheControl: aws.String("no-store")}
	if expectedETag == "" {
		in.IfNoneMatch = aws.String("*")
	} else {
		in.IfMatch = aws.String(expectedETag)
	}
	out, err := p.client.PutObject(ctx, in, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		var response *smithyhttp.ResponseError
		if errors.As(err, &response) {
			switch response.HTTPStatusCode() {
			case http.StatusPreconditionFailed:
				return "", ErrPreconditionFailed
			case http.StatusConflict:
				return "", ErrConditionalConflict
			}
		}
		return "", normalize(err)
	}
	if out == nil || !validUploadETag(aws.ToString(out.ETag)) {
		return "", ErrUnavailable
	}
	return aws.ToString(out.ETag), nil
}
