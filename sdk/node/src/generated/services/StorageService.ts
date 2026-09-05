/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectBucket } from '../models/ObjectBucket.js';
import type { ObjectBucketList } from '../models/ObjectBucketList.js';
import type { ObjectSignedRequest } from '../models/ObjectSignedRequest.js';
import type { ObjectSignRequest } from '../models/ObjectSignRequest.js';
import type { Problem } from '../models/Problem.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class StorageService {
  /**
   * List private object buckets and configured creation capabilities
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
   * Requires deploy:write or admin. Idempotent by app, scope and name, not
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
   * Requires deploy:write or admin. Never recursively deletes data. Nonempty buckets return 409. Repeat after successful deletion returns 404.
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
   * List objects with opaque cursor pagination
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
   * Requires deploy:write or admin. With provider-side versioning this may create a delete marker; version management is not part of this preview.
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
   * GET requires apps:read or admin; PUT requires deploy:write or admin.
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
}
