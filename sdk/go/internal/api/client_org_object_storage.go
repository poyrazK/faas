package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

func orgObjectBucketsPath(organization, slug string) string {
	return "/v1/orgs/" + url.PathEscape(organization) + "/apps/" + url.PathEscape(slug) + "/buckets"
}

func (c *PreviewClient) GetOrgObjectStorageUsage(ctx context.Context, organization string) (ObjectStorageUsageResponse, error) {
	var out ObjectStorageUsageResponse
	err := c.do(ctx, http.MethodGet, "/v1/orgs/"+url.PathEscape(organization)+"/object-storage-usage", nil, &out)
	return out, err
}

func (c *Client) ListOrgObjectBuckets(ctx context.Context, organization, slug string) (ObjectBucketList, error) {
	var out ObjectBucketList
	err := c.do(ctx, http.MethodGet, orgObjectBucketsPath(organization, slug), nil, &out)
	return out, err
}

func (c *Client) CreateOrgObjectBucket(ctx context.Context, organization, slug string, req CreateObjectBucketRequest) (ObjectBucket, error) {
	var out ObjectBucket
	err := c.do(ctx, http.MethodPost, orgObjectBucketsPath(organization, slug), req, &out)
	return out, err
}

func (c *Client) DeleteOrgObjectBucket(ctx context.Context, organization, slug, bucket string) error {
	return c.do(ctx, http.MethodDelete, orgObjectBucketsPath(organization, slug)+"/"+url.PathEscape(bucket), nil, nil)
}

func (c *Client) ListOrgObjectS3Credentials(ctx context.Context, organization, slug, bucket string) (ObjectS3CredentialList, error) {
	var out ObjectS3CredentialList
	path := orgObjectBucketsPath(organization, slug) + "/" + url.PathEscape(bucket) + "/s3-credentials"
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) CreateOrgObjectS3Credential(ctx context.Context, organization, slug, bucket string, req CreateObjectS3CredentialRequest) (ObjectS3CredentialSecret, error) {
	var out ObjectS3CredentialSecret
	path := orgObjectBucketsPath(organization, slug) + "/" + url.PathEscape(bucket) + "/s3-credentials"
	err := c.do(ctx, http.MethodPost, path, req, &out)
	return out, err
}

func (c *Client) RevokeOrgObjectS3Credential(ctx context.Context, organization, slug, bucket, credential string) error {
	path := orgObjectBucketsPath(organization, slug) + "/" + url.PathEscape(bucket) + "/s3-credentials/" + url.PathEscape(credential)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

func (c *Client) ListOrgObjectStorageComputeBindings(ctx context.Context, organization, slug, bucket string) (ObjectStorageComputeBindingList, error) {
	var out ObjectStorageComputeBindingList
	path := orgObjectBucketsPath(organization, slug) + "/" + url.PathEscape(bucket) + "/compute-bindings"
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) CreateOrgObjectStorageComputeBinding(ctx context.Context, organization, slug, bucket string, req CreateObjectStorageComputeBindingRequest) (ObjectStorageComputeBinding, error) {
	var out ObjectStorageComputeBinding
	path := orgObjectBucketsPath(organization, slug) + "/" + url.PathEscape(bucket) + "/compute-bindings"
	err := c.do(ctx, http.MethodPost, path, req, &out)
	return out, err
}

func (c *Client) DeleteOrgObjectStorageComputeBinding(ctx context.Context, organization, slug, bucket, binding string) error {
	path := orgObjectBucketsPath(organization, slug) + "/" + url.PathEscape(bucket) + "/compute-bindings/" + url.PathEscape(binding)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

func (c *Client) RotateOrgObjectStorageComputeBinding(ctx context.Context, organization, slug, bucket, binding string) (ObjectStorageComputeBinding, error) {
	var out ObjectStorageComputeBinding
	path := orgObjectBucketsPath(organization, slug) + "/" + url.PathEscape(bucket) + "/compute-bindings/" + url.PathEscape(binding) + "/rotate"
	err := c.do(ctx, http.MethodPost, path, struct{}{}, &out)
	return out, err
}

func (c *Client) ListOrgObjectBucketAccessGrants(ctx context.Context, organization, slug, bucket string) (ObjectBucketAccessGrantList, error) {
	var out ObjectBucketAccessGrantList
	err := c.do(ctx, http.MethodGet, orgObjectBucketsPath(organization, slug)+"/"+url.PathEscape(bucket)+"/access-grants", nil, &out)
	return out, err
}

func (c *Client) SetOrgObjectBucketAccessGrant(ctx context.Context, organization, slug, bucket, key string, req SetObjectBucketAccessGrantRequest) (ObjectBucketAccessGrant, error) {
	var out ObjectBucketAccessGrant
	err := c.do(ctx, http.MethodPut, orgObjectBucketsPath(organization, slug)+"/"+url.PathEscape(bucket)+"/access-grants/"+url.PathEscape(key), req, &out)
	return out, err
}

func (c *Client) DeleteOrgObjectBucketAccessGrant(ctx context.Context, organization, slug, bucket, key string) error {
	return c.do(ctx, http.MethodDelete, orgObjectBucketsPath(organization, slug)+"/"+url.PathEscape(bucket)+"/access-grants/"+url.PathEscape(key), nil, nil)
}

func (c *Client) ListOrgBucketObjects(ctx context.Context, organization, slug, bucket, prefix, cursor string, limit int) (BucketObjectPage, error) {
	query := url.Values{"prefix": {prefix}, "cursor": {cursor}}
	if limit != 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	var out BucketObjectPage
	err := c.do(ctx, http.MethodGet, orgObjectBucketsPath(organization, slug)+"/"+url.PathEscape(bucket)+"/objects?"+query.Encode(), nil, &out)
	return out, err
}
