/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectBucket } from '../models/ObjectBucket.js';
import type { ObjectBucketAccessGrant } from '../models/ObjectBucketAccessGrant.js';
import type { ObjectBucketAccessGrantList } from '../models/ObjectBucketAccessGrantList.js';
import type { ObjectBucketList } from '../models/ObjectBucketList.js';
import type { ObjectSignedRequest } from '../models/ObjectSignedRequest.js';
import type { ObjectSignRequest } from '../models/ObjectSignRequest.js';
import type { ObjectStorageUsageResponse } from '../models/ObjectStorageUsageResponse.js';
import type { Problem } from '../models/Problem.js';
import type { SetObjectBucketAccessGrantRequest } from '../models/SetObjectBucketAccessGrantRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class StorageService {
  /**
   * List private object buckets and configured creation capabilities
   * storage:manage lists every bucket. storage:read/storage:write keys see only buckets with an explicit grant. Admin and dashboard sessions list every bucket.
   * @returns ObjectBucketList Buckets across this app's environment scopes; backend credentials are never exposed.
   * @returns Problem Authentication, authorization, or storage error
   * @throws ApiError
   */
  public static listObjectBuckets({
    slug,
  }: {
    /**
     * App whose bucket catalog is being managed.
     */
    slug: string,
  }): CancelablePromise<ObjectBucketList | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets',
      path: {
        'slug': slug,
      },
    });
  }
  /**
   * Create a private bucket on the region's current default backend
   * Requires storage:manage or admin. Idempotent by app, scope and name, not
   * by Idempotency-Key. Retry provisioning by submitting the same name and
   * scope. Existing buckets retain their backend when the default changes.
   *
   * @returns ObjectBucket Existing ready bucket
   * @returns Problem Invalid request, access denied, bucket limit/conflict, or provider unavailable
   * @throws ApiError
   */
  public static createObjectBucket({
    slug,
    requestBody,
  }: {
    /**
     * App whose bucket catalog is being managed.
     */
    slug: string,
    requestBody: {
      name: string;
      scope?: string;
      /**
       * Gregale region, not upstream signing region. Omit to use the configured default.
       */
      region?: string;
    },
  }): CancelablePromise<ObjectBucket | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/buckets',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Delete an empty bucket
   * Requires storage:manage or admin. Never recursively deletes data. Nonempty buckets return 409. Repeat after successful deletion returns 404.
   * @returns Problem Access denied, bucket missing/nonempty/busy, or provider unavailable
   * @throws ApiError
   */
  public static deleteObjectBucket({
    slug,
    bucket,
  }: {
    /**
     * App that owns the bucket to remove.
     */
    slug: string,
    /**
     * Identifier of the empty bucket to delete.
     */
    bucket: string,
  }): CancelablePromise<Problem> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/buckets/{bucket}',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
    });
  }
  /**
   * List API-key access grants for a bucket
   * Requires storage:manage or admin. Revoked keys remain visible until their key row is deleted.
   * @returns ObjectBucketAccessGrantList Logical grants; provider credentials are never exposed.
   * @returns Problem Access denied or bucket unavailable
   * @throws ApiError
   */
  public static listObjectBucketAccessGrants({
    slug,
    bucket,
  }: {
    /**
     * App containing the bucket access binding.
     */
    slug: string,
    /**
     * Bucket whose API-key grants are being managed.
     */
    bucket: string,
  }): CancelablePromise<ObjectBucketAccessGrantList | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/access-grants',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
    });
  }
  /**
   * Create or replace an API-key grant for a bucket
   * Requires storage:manage or admin. The target key must be active or in grace and carry the storage scopes needed by the requested permission. Admin keys do not need and cannot receive grants.
   * @returns ObjectBucketAccessGrant Grant created or replaced
   * @returns Problem Invalid request, missing key/bucket, scope mismatch, or access denied
   * @throws ApiError
   */
  public static setObjectBucketAccessGrant({
    slug,
    bucket,
    key,
    requestBody,
  }: {
    /**
     * App that owns the logical bucket.
     */
    slug: string,
    /**
     * Bucket whose API-key grant is being managed.
     */
    bucket: string,
    /**
     * Account API-key identifier.
     */
    key: string,
    requestBody: SetObjectBucketAccessGrantRequest,
  }): CancelablePromise<ObjectBucketAccessGrant | Problem> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/buckets/{bucket}/access-grants/{key}',
      path: {
        'slug': slug,
        'bucket': bucket,
        'key': key,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Revoke an API-key grant for a bucket
   * Requires storage:manage or admin. Revocation takes effect before this response returns; already-signed provider URLs remain valid until their short expiry.
   * @returns Problem Grant or bucket missing, or access denied
   * @throws ApiError
   */
  public static deleteObjectBucketAccessGrant({
    slug,
    bucket,
    key,
  }: {
    /**
     * App that owns the logical bucket.
     */
    slug: string,
    /**
     * Bucket whose API-key grant is being managed.
     */
    bucket: string,
    /**
     * Account API-key identifier.
     */
    key: string,
  }): CancelablePromise<Problem> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/buckets/{bucket}/access-grants/{key}',
      path: {
        'slug': slug,
        'bucket': bucket,
        'key': key,
      },
    });
  }
  /**
   * List objects with opaque cursor pagination
   * Requires storage:read or admin. Non-admin keys also require a read or read_write grant on this bucket.
   * @returns any One page, not a bucket-wide usage or billing total
   * @returns Problem Invalid request, access denied, bucket unavailable, or provider error
   * @throws ApiError
   */
  public static listBucketObjects({
    slug,
    bucket,
    prefix,
    cursor,
    limit = 100,
  }: {
    /**
     * App containing the objects being managed.
     */
    slug: string,
    /**
     * Identifier of the bucket whose objects are being managed.
     */
    bucket: string,
    /**
     * Only return keys starting with this prefix.
     */
    prefix?: string,
    /**
     * Opaque next_cursor from the preceding page.
     */
    cursor?: string,
    /**
     * Maximum objects in this page.
     */
    limit?: number,
  }): CancelablePromise<{
    items: Array<{
      key: string;
      size_bytes: number;
      last_modified: string;
    }>;
    next_cursor?: string;
  } | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/objects',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
      query: {
        'prefix': prefix,
        'cursor': cursor,
        'limit': limit,
      },
    });
  }
  /**
   * Delete one object by exact key
   * Requires storage:write or admin. Non-admin keys also require a write or read_write grant on this bucket. With provider-side versioning this may create a delete marker; version management is not part of this preview.
   * @returns Problem Invalid request, access denied, or provider error
   * @throws ApiError
   */
  public static deleteBucketObject({
    slug,
    bucket,
    key,
  }: {
    /**
     * App containing the objects being managed.
     */
    slug: string,
    /**
     * Identifier of the bucket whose objects are being managed.
     */
    bucket: string,
    /**
     * Exact object key to delete; URL-encode it.
     */
    key: string,
  }): CancelablePromise<Problem> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/buckets/{bucket}/objects',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
      query: {
        'key': key,
      },
    });
  }
  /**
   * Issue a short-lived direct upload or download URL
   * GET requires storage:read or admin; PUT requires storage:write or admin.
   * Non-admin keys also require a matching per-bucket grant.
   * PUT must declare size_bytes, enforced by signed length (or an empty-body
   * digest for zero bytes). These reusable bearer URLs expire within 15
   * minutes and are not retained by the API idempotency cache. Send only
   * returned headers to the URL, never Gregale credentials. In browsers,
   * Content-Length is set by fetch from the File body, not manually.
   *
   * @returns ObjectSignedRequest Temporary capability; Cache-Control no-store
   * @returns Problem Invalid request, access denied, or provider unavailable
   * @throws ApiError
   */
  public static signBucketObject({
    slug,
    bucket,
    requestBody,
  }: {
    /**
     * App authorizing the requested object capability.
     */
    slug: string,
    /**
     * Identifier of the bucket containing the authorized object.
     */
    bucket: string,
    requestBody: ObjectSignRequest,
  }): CancelablePromise<ObjectSignedRequest | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/buckets/{bucket}/signed-url',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read object storage accounting and safety limits
   * Requires usage read scope. Reservations are capacity commitments, not billed usage. Fresh false blocks new signed URLs.
   * @returns ObjectStorageUsageResponse Current account usage; Cache-Control no-store
   * @returns Problem Access denied or accounting unavailable
   * @throws ApiError
   */
  public static getObjectStorageUsage(): CancelablePromise<ObjectStorageUsageResponse | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/object-storage-usage',
    });
  }
}
