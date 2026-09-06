/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CompleteObjectMultipartUploadRequest } from '../models/CompleteObjectMultipartUploadRequest.js';
import type { CreateObjectMultipartUploadRequest } from '../models/CreateObjectMultipartUploadRequest.js';
import type { ObjectBucket } from '../models/ObjectBucket.js';
import type { ObjectBucketAccessGrant } from '../models/ObjectBucketAccessGrant.js';
import type { ObjectBucketAccessGrantList } from '../models/ObjectBucketAccessGrantList.js';
import type { ObjectBucketList } from '../models/ObjectBucketList.js';
import type { ObjectMultipartPartList } from '../models/ObjectMultipartPartList.js';
import type { ObjectMultipartPartSignRequest } from '../models/ObjectMultipartPartSignRequest.js';
import type { ObjectMultipartUpload } from '../models/ObjectMultipartUpload.js';
import type { ObjectMultipartUploadList } from '../models/ObjectMultipartUploadList.js';
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
   * List durable resumable upload sessions
   * Requires storage:write or admin and a matching bucket grant. The provider upload ID is never returned. Use cursor to recover sessions after a client loses its local session identifier.
   * @returns ObjectMultipartUploadList Durable upload sessions; Cache-Control no-store
   * @returns Problem Invalid cursor, access denial, or backend placement unavailable
   * @throws ApiError
   */
  public static listObjectMultipartUploads({
    slug,
    bucket,
    limit = 100,
    cursor,
  }: {
    /**
     * App containing the multipart upload target.
     */
    slug: string,
    /**
     * Identifier of the bucket receiving the multipart object.
     */
    bucket: string,
    /**
     * Maximum number of sessions to return.
     */
    limit?: number,
    /**
     * UUID cursor returned by the previous page.
     */
    cursor?: string,
  }): CancelablePromise<ObjectMultipartUploadList | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/multipart-uploads',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
      query: {
        'limit': limit,
        'cursor': cursor,
      },
    });
  }
  /**
   * Start or recover a resumable multipart upload
   * Requires storage:write or admin and a matching bucket grant. Gregale
   * reserves the complete declared size before creating billable provider
   * parts. A retry with the same live bucket/key, size, and content type
   * returns the existing session; conflicting parameters return 409. The
   * provider upload ID remains private. Sessions expire after 24 hours.
   *
   * @returns ObjectMultipartUpload Existing compatible live session returned
   * @returns Problem Invalid request, stale accounting, capacity limit, access denial, or provider failure
   * @throws ApiError
   */
  public static createObjectMultipartUpload({
    slug,
    bucket,
    requestBody,
  }: {
    /**
     * App containing the multipart upload target.
     */
    slug: string,
    /**
     * Identifier of the bucket receiving the multipart object.
     */
    bucket: string,
    requestBody: CreateObjectMultipartUploadRequest,
  }): CancelablePromise<ObjectMultipartUpload | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/buckets/{bucket}/multipart-uploads',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * List parts confirmed by the storage provider
   * Requires storage:write or admin and a matching bucket grant. Returns provider-confirmed ETags so an interrupted client can resume completion without exposing provider credentials or upload IDs.
   * @returns ObjectMultipartPartList Provider-confirmed uploaded parts; Cache-Control no-store
   * @returns Problem Session missing, not recoverable, access denial, or provider failure
   * @throws ApiError
   */
  public static listObjectMultipartParts({
    slug,
    bucket,
    upload,
    partNumberMarker,
    limit = 1000,
  }: {
    /**
     * App authorizing multipart recovery.
     */
    slug: string,
    /**
     * Bucket containing the provider-confirmed parts.
     */
    bucket: string,
    /**
     * Gregale session whose provider parts are being recovered.
     */
    upload: string,
    /**
     * Return parts after this part number.
     */
    partNumberMarker?: number,
    /**
     * Maximum number of provider parts to return.
     */
    limit?: number,
  }): CancelablePromise<ObjectMultipartPartList | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}/parts',
      path: {
        'slug': slug,
        'bucket': bucket,
        'upload': upload,
      },
      query: {
        'part_number_marker': partNumberMarker,
        'limit': limit,
      },
    });
  }
  /**
   * Read resumable upload state and part layout
   * Requires storage:write or admin and a matching bucket grant. Provider credentials and upload IDs are never returned.
   * @returns ObjectMultipartUpload Durable session state; Cache-Control no-store
   * @returns Problem Session missing, access denied, or backend placement unavailable
   * @throws ApiError
   */
  public static getObjectMultipartUpload({
    slug,
    bucket,
    upload,
  }: {
    /**
     * App containing the durable upload session.
     */
    slug: string,
    /**
     * Bucket containing the multipart session.
     */
    bucket: string,
    /**
     * Gregale multipart session identifier; this is not the provider upload ID.
     */
    upload: string,
  }): CancelablePromise<ObjectMultipartUpload | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}',
      path: {
        'slug': slug,
        'bucket': bucket,
        'upload': upload,
      },
    });
  }
  /**
   * Abort a multipart upload and release provider-side parts
   * Requires storage:write or admin and a matching bucket grant. Repeating an already-finished abort is safe. Failed aborts are retried by the recovery worker.
   * @returns Problem Session busy/completed, access denied, or provider failure
   * @throws ApiError
   */
  public static abortObjectMultipartUpload({
    slug,
    bucket,
    upload,
  }: {
    /**
     * App containing the durable upload session.
     */
    slug: string,
    /**
     * Bucket containing the multipart session.
     */
    bucket: string,
    /**
     * Gregale multipart session identifier; this is not the provider upload ID.
     */
    upload: string,
  }): CancelablePromise<Problem> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}',
      path: {
        'slug': slug,
        'bucket': bucket,
        'upload': upload,
      },
    });
  }
  /**
   * Issue an exact-length direct upload URL for one part
   * Requires storage:write or admin and a matching bucket grant. The URL
   * binds the server-calculated byte length for this part and expires within
   * 15 minutes. Upload it without Gregale credentials and retain the ETag
   * response header for completion. Every issued part URL consumes the
   * authorization safety budget.
   *
   * @returns ObjectSignedRequest Temporary provider capability; Cache-Control no-store
   * @returns Problem Invalid part, expired session, stale accounting, access denial, or provider failure
   * @throws ApiError
   */
  public static signObjectMultipartPart({
    slug,
    bucket,
    upload,
    part,
    requestBody,
  }: {
    /**
     * App authorizing the multipart part capability.
     */
    slug: string,
    /**
     * Bucket receiving this upload part.
     */
    bucket: string,
    /**
     * Gregale multipart session identifier.
     */
    upload: string,
    /**
     * One-based part number from the session layout.
     */
    part: number,
    requestBody: ObjectMultipartPartSignRequest,
  }): CancelablePromise<ObjectSignedRequest | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}/parts/{part}/signed-url',
      path: {
        'slug': slug,
        'bucket': bucket,
        'upload': upload,
        'part': part,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Assemble all uploaded parts into the final object
   * Requires storage:write or admin and a matching bucket grant. Supply one
   * ETag for every part in ascending order. Completion intent is persisted
   * before contacting the provider and recovered after crashes. An identical
   * retry after completion returns the completed session without repeating
   * the provider operation.
   *
   * @returns ObjectMultipartUpload Object completed; Cache-Control no-store
   * @returns Problem Invalid/missing parts, expired or conflicting session, access denial, or provider failure
   * @throws ApiError
   */
  public static completeObjectMultipartUpload({
    slug,
    bucket,
    upload,
    requestBody,
  }: {
    /**
     * App finalizing the multipart upload.
     */
    slug: string,
    /**
     * Bucket receiving the completed object.
     */
    bucket: string,
    /**
     * Gregale multipart session identifier to complete.
     */
    upload: string,
    requestBody: CompleteObjectMultipartUploadRequest,
  }): CancelablePromise<ObjectMultipartUpload | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}/complete',
      path: {
        'slug': slug,
        'bucket': bucket,
        'upload': upload,
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
