package objectstorage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectVersionTagger = (*S3)(nil)

func validTaggingTarget(key, version string) bool {
	return ValidKey(key) && (version == "" || validNativeVersionID(version))
}

func (p *S3) GetObjectVersionTags(ctx context.Context, bucket, key, version string) (ObjectTaggingResult, error) {
	result := ObjectTaggingResult{}
	if !validTaggingTarget(key, version) {
		return result, ErrInvalid
	}
	out, err := p.client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: stringPtrOrNil(version)}, func(o *s3.Options) {
		o.RetryMaxAttempts = 1
		o.HTTPClient = taggingReadClient{base: o.HTTPClient}
	})
	if err != nil {
		return result, normalizeTaggingError(err)
	}
	if out == nil {
		return result, ErrUnavailable
	}
	result.ProviderVersionID, err = taggingResponseVersion(out.ResultMetadata, aws.ToString(out.VersionId), version, http.StatusOK)
	if err != nil {
		return ObjectTaggingResult{}, err
	}
	result.Tags = make(map[string]string, len(out.TagSet))
	for _, tag := range out.TagSet {
		key := aws.ToString(tag.Key)
		if _, exists := result.Tags[key]; exists || tag.Value == nil {
			return ObjectTaggingResult{}, ErrUnavailable
		}
		result.Tags[key] = aws.ToString(tag.Value)
	}
	if ValidateObjectMetadata(ObjectMetadata{Tags: result.Tags}) != nil {
		return ObjectTaggingResult{}, ErrUnavailable
	}
	return result, nil
}

func (p *S3) PutObjectVersionTags(ctx context.Context, bucket, key, version string, tags map[string]string) (ObjectTaggingResult, error) {
	if !validTaggingTarget(key, version) || ValidateObjectMetadata(ObjectMetadata{Tags: tags}) != nil {
		return ObjectTaggingResult{}, ErrInvalid
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	tagSet := make([]types.Tag, 0, len(tags))
	for _, key := range keys {
		tagSet = append(tagSet, types.Tag{Key: aws.String(key), Value: aws.String(tags[key])})
	}
	out, err := p.client.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: stringPtrOrNil(version), Tagging: &types.Tagging{TagSet: tagSet}}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return ObjectTaggingResult{}, normalizeTaggingError(err)
	}
	if out == nil {
		return ObjectTaggingResult{}, ErrUnavailable
	}
	id, err := taggingResponseVersion(out.ResultMetadata, aws.ToString(out.VersionId), version, http.StatusOK)
	return ObjectTaggingResult{ProviderVersionID: id}, err
}

func (p *S3) DeleteObjectVersionTags(ctx context.Context, bucket, key, version string) (ObjectTaggingResult, error) {
	if !validTaggingTarget(key, version) {
		return ObjectTaggingResult{}, ErrInvalid
	}
	out, err := p.client.DeleteObjectTagging(ctx, &s3.DeleteObjectTaggingInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: stringPtrOrNil(version)}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return ObjectTaggingResult{}, normalizeTaggingError(err)
	}
	if out == nil {
		return ObjectTaggingResult{}, ErrUnavailable
	}
	id, err := taggingResponseVersion(out.ResultMetadata, aws.ToString(out.VersionId), version, http.StatusNoContent)
	return ObjectTaggingResult{ProviderVersionID: id}, err
}

func taggingResponseVersion(metadata middleware.Metadata, returned, expected string, status int) (string, error) {
	response, ok := awsmiddleware.GetRawResponse(metadata).(*smithyhttp.Response)
	if !ok || response == nil || response.Response == nil || response.StatusCode != status || len(response.Header.Values("X-Amz-Version-Id")) > 1 || len(response.Header.Values("X-Amz-Delete-Marker")) > 1 || response.Header.Get("X-Amz-Delete-Marker") != "" && response.Header.Get("X-Amz-Delete-Marker") != "false" || returned != "" && !validNativeVersionID(returned) {
		return "", ErrUnavailable
	}
	if expected == "null" && returned == "" {
		returned = "null"
	}
	if expected != "" && returned != expected {
		return "", ErrUnavailable
	}
	return returned, nil
}

func normalizeTaggingError(err error) error {
	var service smithy.APIError
	var response *smithyhttp.ResponseError
	if errors.As(err, &service) {
		switch service.ErrorCode() {
		case "InvalidTag", "MalformedXML":
			return ErrInvalid
		case "OperationAborted":
			return ErrConflict
		case "MethodNotAllowed":
			if errors.As(err, &response) && response.HTTPStatusCode() == http.StatusMethodNotAllowed {
				return ErrObjectNotTaggable
			}
		}
	}
	return normalizeVersionHistoryError(err)
}

type taggingReadClient struct{ base aws.HTTPClient }

func (c taggingReadClient) Do(r *http.Request) (*http.Response, error) {
	response, err := c.base.Do(r)
	if err != nil || response == nil || response.StatusCode != http.StatusOK {
		return response, err
	}
	if response.Body == nil {
		return nil, ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, api.MaxObjectTaggingBodyBytes+1))
	_ = response.Body.Close()
	if err != nil {
		return nil, ErrUnavailable
	}
	if _, err = ParseObjectTaggingXML(body); err != nil {
		return nil, ErrUnavailable
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}
