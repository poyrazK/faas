/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CompleteObjectMultipartUploadRequest } from '../models/CompleteObjectMultipartUploadRequest.js';
import type { CreateObjectMultipartUploadRequest } from '../models/CreateObjectMultipartUploadRequest.js';
import type { CreateObjectS3CredentialRequest } from '../models/CreateObjectS3CredentialRequest.js';
import type { CreateObjectStorageComputeBindingRequest } from '../models/CreateObjectStorageComputeBindingRequest.js';
import type { CreateObjectUploadRouteRequest } from '../models/CreateObjectUploadRouteRequest.js';
import type { ObjectBucket } from '../models/ObjectBucket.js';
import type { ObjectBucketAccessGrant } from '../models/ObjectBucketAccessGrant.js';
import type { ObjectBucketAccessGrantList } from '../models/ObjectBucketAccessGrantList.js';
import type { ObjectBucketLifecycle } from '../models/ObjectBucketLifecycle.js';
import type { ObjectBucketLifecycleRequest } from '../models/ObjectBucketLifecycleRequest.js';
import type { ObjectBucketList } from '../models/ObjectBucketList.js';
import type { ObjectBucketNotifications } from '../models/ObjectBucketNotifications.js';
import type { ObjectBucketNotificationsRequest } from '../models/ObjectBucketNotificationsRequest.js';
import type { ObjectBucketVersioning } from '../models/ObjectBucketVersioning.js';
import type { ObjectBucketVersioningRequest } from '../models/ObjectBucketVersioningRequest.js';
import type { ObjectCapacityReconciliation } from '../models/ObjectCapacityReconciliation.js';
import type { ObjectDeletion } from '../models/ObjectDeletion.js';
import type { ObjectDeletionRequest } from '../models/ObjectDeletionRequest.js';
import type { ObjectEncryptionCapabilities } from '../models/ObjectEncryptionCapabilities.js';
import type { ObjectLifecycleScan } from '../models/ObjectLifecycleScan.js';
import type { ObjectMultipartPartList } from '../models/ObjectMultipartPartList.js';
import type { ObjectMultipartPartSignRequest } from '../models/ObjectMultipartPartSignRequest.js';
import type { ObjectMultipartUpload } from '../models/ObjectMultipartUpload.js';
import type { ObjectMultipartUploadList } from '../models/ObjectMultipartUploadList.js';
import type { ObjectS3CredentialList } from '../models/ObjectS3CredentialList.js';
import type { ObjectS3CredentialSecret } from '../models/ObjectS3CredentialSecret.js';
import type { ObjectSignedRequest } from '../models/ObjectSignedRequest.js';
import type { ObjectSignRequest } from '../models/ObjectSignRequest.js';
import type { ObjectStorageComputeBinding } from '../models/ObjectStorageComputeBinding.js';
import type { ObjectStorageComputeBindingList } from '../models/ObjectStorageComputeBindingList.js';
import type { ObjectStorageUsageResponse } from '../models/ObjectStorageUsageResponse.js';
import type { ObjectTaggingRequest } from '../models/ObjectTaggingRequest.js';
import type { ObjectTaggingResult } from '../models/ObjectTaggingResult.js';
import type { ObjectUploadRoute } from '../models/ObjectUploadRoute.js';
import type { ObjectUploadRouteList } from '../models/ObjectUploadRouteList.js';
import type { ObjectVersionDeleteResult } from '../models/ObjectVersionDeleteResult.js';
import type { ObjectWriteReceipt } from '../models/ObjectWriteReceipt.js';
import type { ObjectWriteReceiptList } from '../models/ObjectWriteReceiptList.js';
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
   * Create a bucket on the region's current default backend
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
      /**
       * Serve objects anonymously on the app hostname.
       */
      public?: boolean;
      /**
       * Immutable app path mounted when public is true.
       */
      serve_at?: string;
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
   * List policy-controlled upload routes
   * Routes are served by Gregale's public edge and do not wake the application. Requires storage:read, storage:write, storage:manage, or admin.
   * @returns ObjectUploadRouteList Upload route policies without provider credentials.
   * @returns Problem Unable to list upload routes because of an authentication, authorization, or storage failure.
   * @throws ApiError
   */
  public static listObjectUploadRoutes({
    slug,
  }: {
    /**
     * App whose edge upload routes are being managed.
     */
    slug: string,
  }): CancelablePromise<ObjectUploadRouteList | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/upload-routes',
      path: {
        'slug': slug,
      },
    });
  }
  /**
   * Create or update an authenticated upload route
   * Declares POST /uploads/{name}. Gregale authenticates an API key, generates an owner-scoped object key, enforces the byte/content-type policy, streams directly to the selected provider, and records a completion receipt. The public upload endpoint accepts an optional Idempotency-Key header (up to 128 bytes) to replay a completed or failed attempt without repeating provider work; conflicting request metadata returns 409.
   * @returns ObjectUploadRoute Existing upload route updated
   * @returns Problem Invalid policy, bucket unavailable, access denied, or provider unavailable
   * @throws ApiError
   */
  public static createObjectUploadRoute({
    slug,
    requestBody,
  }: {
    /**
     * App whose edge upload routes are being managed.
     */
    slug: string,
    requestBody: CreateObjectUploadRouteRequest,
  }): CancelablePromise<ObjectUploadRoute | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/upload-routes',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Delete an authenticated upload route
   * Stops new uploads immediately. Existing objects are not deleted.
   * @returns Problem Route missing, access denied, or storage unavailable
   * @throws ApiError
   */
  public static deleteObjectUploadRoute({
    slug,
    route,
  }: {
    /**
     * App owning the edge upload route.
     */
    slug: string,
    /**
     * Stable upload route name used at /uploads/{route}.
     */
    route: string,
  }): CancelablePromise<Problem> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/upload-routes/{route}',
      path: {
        'slug': slug,
        'route': route,
      },
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
     * App whose API-key bucket grant is being managed.
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
     * App whose API-key bucket grant is being managed.
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
   * List active Gregale S3 credentials for a bucket
   * Requires storage:manage or admin. Secret access keys are never returned by this endpoint.
   * @returns ObjectS3CredentialList Active bucket-scoped S3 credentials; Cache-Control no-store
   * @returns Problem S3 credential listing denied or its bucket is unavailable
   * @throws ApiError
   */
  public static listObjectS3Credentials({
    slug,
    bucket,
  }: {
    /**
     * App whose new S3 credential will be bucket-scoped.
     */
    slug: string,
    /**
     * Bucket receiving the S3 credential.
     */
    bucket: string,
  }): CancelablePromise<ObjectS3CredentialList | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/s3-credentials',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
    });
  }
  /**
   * Create a bucket-scoped credential for s3.gregale.dev
   * The secret access key is returned exactly once, sealed at rest, and never recoverable through the control-plane API. At most ten active credentials may exist per bucket.
   * @returns Problem Invalid request, credential limit, unavailable sealing key, or access denied
   * @returns ObjectS3CredentialSecret One-time S3 credential response; Cache-Control no-store
   * @throws ApiError
   */
  public static createObjectS3Credential({
    slug,
    bucket,
    requestBody,
  }: {
    /**
     * App whose new S3 credential will be bucket-scoped.
     */
    slug: string,
    /**
     * Bucket receiving the S3 credential.
     */
    bucket: string,
    requestBody: CreateObjectS3CredentialRequest,
  }): CancelablePromise<Problem | ObjectS3CredentialSecret> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/buckets/{bucket}/s3-credentials',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Revoke a Gregale S3 credential
   * Revocation is immediate for new requests at s3.gregale.dev. Existing provider-signed internal requests are never exposed to the customer.
   * @returns Problem Credential or bucket missing, or access denied
   * @throws ApiError
   */
  public static revokeObjectS3Credential({
    slug,
    bucket,
    credential,
  }: {
    /**
     * App whose S3 credential is being revoked.
     */
    slug: string,
    /**
     * Bucket whose credential is being revoked.
     */
    bucket: string,
    /**
     * Gregale S3 credential identifier.
     */
    credential: string,
  }): CancelablePromise<Problem> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/buckets/{bucket}/s3-credentials/{credential}',
      path: {
        'slug': slug,
        'bucket': bucket,
        'credential': credential,
      },
    });
  }
  /**
   * List compute bindings for a bucket
   * Requires storage:manage or admin. Secret values are never returned; only the sealed app-secret names are listed.
   * @returns ObjectStorageComputeBindingList Active compute bindings; Cache-Control no-store
   * @returns Problem Binding listing denied or its bucket is unavailable
   * @throws ApiError
   */
  public static listObjectStorageComputeBindings({
    slug,
    bucket,
  }: {
    /**
     * App receiving the sealed storage connection settings.
     */
    slug: string,
    /**
     * Bucket exposed to the compute workload.
     */
    bucket: string,
  }): CancelablePromise<ObjectStorageComputeBindingList | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/compute-bindings',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
    });
  }
  /**
   * Bind a bucket to an app's compute environment
   * Creates a bucket-scoped Gregale S3 credential and injects endpoint, region, bucket, access-key, secret-key, and addressing settings as sealed app secrets. The secret values are never returned.
   * @returns Problem Invalid request, secret quota/conflict, unavailable sealing key, or access denied
   * @returns ObjectStorageComputeBinding Compute binding created; Cache-Control no-store
   * @throws ApiError
   */
  public static createObjectStorageComputeBinding({
    slug,
    bucket,
    requestBody,
  }: {
    /**
     * App receiving the sealed storage connection settings.
     */
    slug: string,
    /**
     * Bucket exposed to the compute workload.
     */
    bucket: string,
    requestBody: CreateObjectStorageComputeBindingRequest,
  }): CancelablePromise<Problem | ObjectStorageComputeBinding> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/buckets/{bucket}/compute-bindings',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Revoke a compute binding
   * Revokes the bucket-scoped S3 credential before removing its sealed app secrets. The operation is idempotent after revocation.
   * @returns Problem Binding or bucket missing, or access denied
   * @throws ApiError
   */
  public static deleteObjectStorageComputeBinding({
    slug,
    bucket,
    binding,
  }: {
    /**
     * App owning the compute binding.
     */
    slug: string,
    /**
     * Bucket attached to the compute binding.
     */
    bucket: string,
    /**
     * Opaque compute binding identifier.
     */
    binding: string,
  }): CancelablePromise<Problem> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/buckets/{bucket}/compute-bindings/{binding}',
      path: {
        'slug': slug,
        'bucket': bucket,
        'binding': binding,
      },
    });
  }
  /**
   * Rotate a compute binding credential
   * Atomically replaces the bucket-scoped access key and two sealed app secrets while keeping the binding and environment variable names stable. For a live app, the previous key remains valid until the rolling runtime refresh drains old instances; rotation_pending is true in the response. Retrying during a pending rotation requeues the same refresh. Secret values are never returned.
   * @returns ObjectStorageComputeBinding Binding rotated; Cache-Control no-store
   * @returns Problem Binding unavailable, sealing key unavailable, or access denied
   * @throws ApiError
   */
  public static rotateObjectStorageComputeBinding({
    slug,
    bucket,
    binding,
  }: {
    /**
     * App whose workload receives the rotated credential.
     */
    slug: string,
    /**
     * Logical bucket whose access key is being rotated.
     */
    bucket: string,
    /**
     * Binding identifier retained across credential rotations.
     */
    binding: string,
  }): CancelablePromise<ObjectStorageComputeBinding | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/buckets/{bucket}/compute-bindings/{binding}/rotate',
      path: {
        'slug': slug,
        'bucket': bucket,
        'binding': binding,
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
   * Requires storage:write or admin and a bucket write grant. Current-object deletion is declined when retained versions, native inventories or a versioning transition require marker admission. Use permanent immutable version deletion for retained data or markers.
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
   * Read the tags of a current or selected object
   * Requires storage read scope and a bucket read grant. Public version IDs remain stable after restart. Native provider version IDs are never exposed.
   * @returns ObjectTaggingResult Stored object tags and public version identity; response is never cached
   * @returns Problem Tag read rejected for invalid ownership, permission, capability, input, budget or provider response
   * @throws ApiError
   */
  public static getObjectBucketTags({
    slug,
    bucket,
    key,
    versionId,
  }: {
    /**
     * Application containing the object whose tags are requested.
     */
    slug: string,
    /**
     * Logical bucket used for this object tag operation.
     */
    bucket: string,
    /**
     * Exact object key; URL-encode it.
     */
    key: string,
    /**
     * Omit for the current object; use null or an owned public version UUID for a selected version.
     */
    versionId?: string,
  }): CancelablePromise<ObjectTaggingResult | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/objects/tags',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
      query: {
        'key': key,
        'version_id': versionId,
      },
    });
  }
  /**
   * Replace the complete tag set of a current or selected object
   * Requires storage write scope and a bucket write grant. Tags change in place and preserve private write-completion metadata. S3 dispatches once per request; after an uncertain acknowledgment, read the selected version or explicitly retry the desired tag set. No data version or storage reservation is created.
   * @returns ObjectTaggingResult Replacement tag set acknowledged by the provider; public identity is retained
   * @returns Problem Tag replacement failed validation, authorization or admission, or has an uncertain provider acknowledgment
   * @throws ApiError
   */
  public static putObjectBucketTags({
    slug,
    bucket,
    key,
    requestBody,
    versionId,
  }: {
    /**
     * Application containing the object whose tags are requested.
     */
    slug: string,
    /**
     * Logical bucket used for this object tag operation.
     */
    bucket: string,
    /**
     * Exact object key; URL-encode it.
     */
    key: string,
    requestBody: ObjectTaggingRequest,
    /**
     * Omit for the current object; use null or an owned public version UUID for a selected version.
     */
    versionId?: string,
  }): CancelablePromise<ObjectTaggingResult | Problem> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/buckets/{bucket}/objects/tags',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
      query: {
        'key': key,
        'version_id': versionId,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Remove all tags from a current or selected object
   * Requires storage write scope and a bucket write grant. Returns an empty tag set after acknowledgment. An uncertain response does not trigger automatic mutation replay. Tags change in place; storage capacity is unchanged.
   * @returns ObjectTaggingResult Empty tag set after acknowledged removal; public version identity is retained
   * @returns Problem Tag removal denied or unsupported, safety budget exhausted, or provider acknowledgment unavailable
   * @throws ApiError
   */
  public static deleteObjectBucketTags({
    slug,
    bucket,
    key,
    versionId,
  }: {
    /**
     * Application containing the object whose tags are requested.
     */
    slug: string,
    /**
     * Logical bucket used for this object tag operation.
     */
    bucket: string,
    /**
     * Exact object key; URL-encode it.
     */
    key: string,
    /**
     * Omit for the current object; use null or an owned public version UUID for a selected version.
     */
    versionId?: string,
  }): CancelablePromise<ObjectTaggingResult | Problem> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/buckets/{bucket}/objects/tags',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
      query: {
        'key': key,
        'version_id': versionId,
      },
    });
  }
  /**
   * Permanently delete an immutable object version or delete marker
   * Requires storage write scope and the bucket write grant. The public version ID must belong to this bucket and exact key. Retries address the same immutable version, including after restart or an uncertain provider acknowledgment. Deleting a marker can reveal older data. Mutable null deletion uses a durable single-attempt intent; retry with X-Gregale-Delete-Id or use the deletion receipt API. Quota is reclaimed only through verified capacity inventory.
   * @returns ObjectVersionDeleteResult Deleted or already removed immutable version; Cache-Control no-store
   * @returns Problem Invalid or unowned version, unsupported provider, access denied or uncertain provider response
   * @throws ApiError
   */
  public static deleteObjectBucketVersion({
    slug,
    bucket,
    key,
    versionId,
  }: {
    /**
     * App owning the logical bucket.
     */
    slug: string,
    /**
     * Logical bucket owning the selected version.
     */
    bucket: string,
    /**
     * Exact key owning the selected version.
     */
    key: string,
    /**
     * Owned public version UUID or the mutable null selector; native provider IDs are not accepted.
     */
    versionId: string,
  }): CancelablePromise<ObjectVersionDeleteResult | Problem> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/buckets/{bucket}/objects/versions',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
      query: {
        'key': key,
        'version_id': versionId,
      },
    });
  }
  /**
   * Delete the current object or an owned version with a durable receipt
   * Requires storage write scope and the bucket write grant. Reuse the request ID with the same key and selector for retries. Mutable intents dispatch at most once; recovery may retry only the exact owned immutable version. Ordinary deletes in Enabled buckets create an accounted marker. Pending attempts fence writes, configuration and inventories until positive acknowledgment or unique completion proof; elapsed time and absence never settle a dispatched mutation.
   * @returns ObjectDeletion Completed or failed receipt replay
   * @returns Problem Invalid request, access denied, unavailable accounting or conflicting mutation
   * @throws ApiError
   */
  public static createObjectDeletion({
    slug,
    bucket,
    requestBody,
  }: {
    /**
     * Application whose object deletion will be journaled.
     */
    slug: string,
    /**
     * Logical bucket owning the deletion.
     */
    bucket: string,
    requestBody: ObjectDeletionRequest,
  }): CancelablePromise<ObjectDeletion | Problem> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/buckets/{bucket}/objects/deletions',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read a durable deletion receipt
   * Requires storage write scope and the bucket write grant. Returns only the bucket-owned receipt and public identities. Cache-Control no-store.
   * @returns ObjectDeletion Persisted deletion state
   * @returns Problem Access denied or receipt does not belong to this bucket
   * @throws ApiError
   */
  public static getObjectDeletion({
    slug,
    bucket,
    deletion,
  }: {
    /**
     * Application associated with the requested deletion receipt.
     */
    slug: string,
    /**
     * Bucket to which the durable deletion receipt belongs.
     */
    bucket: string,
    /**
     * Durable deletion request ID.
     */
    deletion: string,
  }): CancelablePromise<ObjectDeletion | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/objects/deletions/{deletion}',
      path: {
        'slug': slug,
        'bucket': bucket,
        'deletion': deletion,
      },
    });
  }
  /**
   * List enrolled encryption algorithms and owned key references
   * Requires storage write scope and the bucket write grant. Returns only the bucket owner's enrolled Gregale key references for explicit S3 PUT, copy and multipart initialization. Discovery makes no provider requests and does not establish key health or effective native permissions. Cache-Control no-store. Available while new storage ingress is disabled.
   * @returns ObjectEncryptionCapabilities Enrolled algorithms and owned key references
   * @returns Problem Access denied or unavailable bucket placement
   * @throws ApiError
   */
  public static getObjectBucketEncryptionCapabilities({
    slug,
    bucket,
  }: {
    /**
     * App whose bucket encryption enrollment is being discovered.
     */
    slug: string,
    /**
     * Logical bucket whose enrolled encryption options are listed.
     */
    bucket: string,
  }): CancelablePromise<ObjectEncryptionCapabilities | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/encryption-capabilities',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
    });
  }
  /**
   * Read bucket versioning configuration and cutover progress
   * Requires storage manage scope and the bucket write grant. Observes provider truth; discovering native versioning fences writes until a propagated, verified inventory accounts for all versions. Cache-Control no-store.
   * @returns ObjectBucketVersioning Observed configuration and persisted progress
   * @returns Problem Access denied, unavailable bucket or unsupported provider
   * @throws ApiError
   */
  public static getObjectBucketVersioning({
    slug,
    bucket,
  }: {
    /**
     * App whose bucket configuration is being read or changed.
     */
    slug: string,
    /**
     * Logical bucket whose versioning is configured.
     */
    bucket: string,
  }): CancelablePromise<ObjectBucketVersioning | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/versioning',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
    });
  }
  /**
   * Request durable bucket versioning configuration
   * Requires storage manage scope and the bucket write grant. Enabled and Suspended are supported on capable S3 backends. Opposite targets conflict until the current transition is ready. New writes and bucket deletion remain fenced while existing work drains, configuration propagates for at least fifteen minutes and a complete all-version inventory commits. Unresolved legacy write grants reject the request. Cancellation cannot reopen writes. Suspension retains all-version accounting. MFA Delete changes are unsupported.
   * @returns Problem Invalid status, busy bucket, unresolved legacy writes or unsupported provider
   * @returns ObjectBucketVersioning Durable intent recorded; inspect GET for progress
   * @throws ApiError
   */
  public static putObjectBucketVersioning({
    slug,
    bucket,
    requestBody,
  }: {
    /**
     * App whose bucket configuration is being read or changed.
     */
    slug: string,
    /**
     * Logical bucket whose versioning is configured.
     */
    bucket: string,
    requestBody: ObjectBucketVersioningRequest,
  }): CancelablePromise<Problem | ObjectBucketVersioning> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/buckets/{bucket}/versioning',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Read durable bucket notification rules
   * Requires storage manage scope and bucket write access. Reads owned notification intent without a provider call, including when ingress is disabled. Initial configuration has revision zero and no notification rules.
   * @returns ObjectBucketNotifications Persisted notification configuration; Cache-Control no-store
   * @returns Problem Notification policy access denied or the owned bucket was not found
   * @throws ApiError
   */
  public static getObjectBucketNotifications({
    slug,
    bucket,
  }: {
    /**
     * App whose notification policy is inspected.
     */
    slug: string,
    /**
     * Bucket whose notification configuration is managed.
     */
    bucket: string,
  }): CancelablePromise<ObjectBucketNotifications | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/notifications',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
    });
  }
  /**
   * Replace durable bucket notification rules
   * Requires storage manage scope and the bucket write grant. Atomically replaces notification rules and validates Gregale-owned function or queue ARNs in this account and region. Empty rules clear intent. Accepted events retain captured destinations. Nonempty configuration requires storage ingress to be enabled.
   * @returns ObjectBucketNotifications Normalized notification destinations, filters and configuration revision
   * @returns Problem Invalid rule replacement, unavailable destination or unsupported event
   * @throws ApiError
   */
  public static putObjectBucketNotifications({
    slug,
    bucket,
    requestBody,
  }: {
    /**
     * App whose notification policy is inspected.
     */
    slug: string,
    /**
     * Bucket whose notification configuration is managed.
     */
    bucket: string,
    requestBody: ObjectBucketNotificationsRequest,
  }): CancelablePromise<ObjectBucketNotifications | Problem> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/buckets/{bucket}/notifications',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Remove bucket notification rules
   * Requires storage manage scope and the bucket write grant. Clears future routing while accepted events retain captured destinations. Available while storage ingress is disabled.
   * @returns ObjectBucketNotifications Cleared notification intent with its retained revision
   * @returns Problem Policy removal denied or invalid ownership
   * @throws ApiError
   */
  public static deleteObjectBucketNotifications({
    slug,
    bucket,
  }: {
    /**
     * App whose notification policy is inspected.
     */
    slug: string,
    /**
     * Bucket whose notification configuration is managed.
     */
    bucket: string,
  }): CancelablePromise<ObjectBucketNotifications | Problem> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/buckets/{bucket}/notifications',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
    });
  }
  /**
   * Read durable bucket lifecycle rules
   * Requires storage manage scope and the bucket write grant. Reads durable policy without contacting the provider; available while storage ingress is disabled. An absent policy has revision zero and empty rules.
   * @returns ObjectBucketLifecycle Persisted lifecycle configuration; Cache-Control no-store
   * @returns Problem Policy access denied or owned bucket missing
   * @throws ApiError
   */
  public static getObjectBucketLifecycle({
    slug,
    bucket,
  }: {
    /**
     * App whose lifecycle policy is inspected.
     */
    slug: string,
    /**
     * Bucket whose lifecycle configuration is managed.
     */
    bucket: string,
  }): CancelablePromise<ObjectBucketLifecycle | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/lifecycle',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
    });
  }
  /**
   * Replace durable bucket lifecycle rules
   * Requires storage manage scope and the bucket write grant. Replaces the entire policy with one to one thousand validated rules on a capable backend. Expiration, noncurrent expiration and abandoned multipart cleanup use Gregale journals and verified accounting. Transitions and object size predicates are unsupported. A live discovery lease returns conflict. New storage ingress must be enabled.
   * @returns ObjectBucketLifecycle Normalized replacement rules and revision
   * @returns Problem Invalid rule replacement, live scan conflict or unsupported backend
   * @throws ApiError
   */
  public static putObjectBucketLifecycle({
    slug,
    bucket,
    requestBody,
  }: {
    /**
     * App whose lifecycle policy is inspected.
     */
    slug: string,
    /**
     * Bucket whose lifecycle configuration is managed.
     */
    bucket: string,
    requestBody: ObjectBucketLifecycleRequest,
  }): CancelablePromise<ObjectBucketLifecycle | Problem> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/buckets/{bucket}/lifecycle',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Remove bucket lifecycle rules
   * Requires storage manage scope and the bucket write grant. Clears rules and cancels unclaimed discovery. Already admitted deletions and multipart aborts continue to verified completion. Available while ingress is disabled. A live discovery lease returns conflict.
   * @returns ObjectBucketLifecycle Empty rules with retained policy revision
   * @returns Problem Policy removal denied or blocked by a live scan lease
   * @throws ApiError
   */
  public static deleteObjectBucketLifecycle({
    slug,
    bucket,
  }: {
    /**
     * App whose lifecycle policy is inspected.
     */
    slug: string,
    /**
     * Bucket whose lifecycle configuration is managed.
     */
    bucket: string,
  }): CancelablePromise<ObjectBucketLifecycle | Problem> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/buckets/{bucket}/lifecycle',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
    });
  }
  /**
   * Start or resume due lifecycle discovery
   * Requires storage manage scope and the bucket write grant. Creates a due scan or returns existing active discovery. Requires ingress enabled and an enabled policy; the hourly scan interval still applies. Conflict means the next scan is not due. Completed discovery does not prove that admitted deletions or aborts have finished.
   * @returns Problem Discovery disabled, active policy missing or next scan not due
   * @returns ObjectLifecycleScan Accepted active discovery scan; inspect its progress
   * @throws ApiError
   */
  public static createObjectLifecycleScan({
    slug,
    bucket,
  }: {
    /**
     * App requesting lifecycle discovery.
     */
    slug: string,
    /**
     * Bucket requesting due lifecycle discovery.
     */
    bucket: string,
  }): CancelablePromise<Problem | ObjectLifecycleScan> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/buckets/{bucket}/lifecycle/scans',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
    });
  }
  /**
   * Read lifecycle discovery progress
   * Requires storage manage scope and the bucket write grant. Returns owned persisted progress without provider credentials, native identifiers, cursors or lease tokens. Available while ingress is disabled.
   * @returns ObjectLifecycleScan Recorded discovery phase and counters
   * @returns Problem Scan access denied or owned scan not found
   * @throws ApiError
   */
  public static getObjectLifecycleScan({
    slug,
    bucket,
    scan,
  }: {
    /**
     * App owning the recorded lifecycle scan.
     */
    slug: string,
    /**
     * Bucket associated with this lifecycle scan.
     */
    bucket: string,
    /**
     * Owned lifecycle discovery identifier.
     */
    scan: string,
  }): CancelablePromise<ObjectLifecycleScan | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/lifecycle/scans/{scan}',
      path: {
        'slug': slug,
        'bucket': bucket,
        'scan': scan,
      },
    });
  }
  /**
   * Request safe capacity reconciliation
   * Requires storage write scope and the bucket write grant. Returns the existing active job on repeat requests. Pauses new bucket writes until cancellation or a terminal outcome. Pending or untracked writes cannot be force-refunded; only a complete fenced inventory can reclaim capacity. Available with storage disabled or spent budgets.
   * @returns Problem Missing bucket, live multipart sessions, or access denied
   * @returns ObjectCapacityReconciliation Durable reconciliation requested; Cache-Control no-store
   * @throws ApiError
   */
  public static createObjectCapacityReconciliation({
    slug,
    bucket,
  }: {
    /**
     * App requesting a fenced bucket inventory.
     */
    slug: string,
    /**
     * Bucket whose reserved capacity should be reconciled.
     */
    bucket: string,
  }): CancelablePromise<Problem | ObjectCapacityReconciliation> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/buckets/{bucket}/capacity-reconciliations',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
    });
  }
  /**
   * Inspect capacity reconciliation
   * Returns progress, pending-write count and reclaimed capacity under storage write scope and the bucket write grant. Reads remain available with storage disabled or spent budgets. Billing and monthly authorization counts are unchanged.
   * @returns ObjectCapacityReconciliation Reconciliation status; Cache-Control no-store
   * @returns Problem Reconciliation missing or access denied
   * @throws ApiError
   */
  public static getObjectCapacityReconciliation({
    slug,
    bucket,
    reconciliation,
  }: {
    /**
     * App owning the reconciliation job.
     */
    slug: string,
    /**
     * Bucket associated with the reconciliation.
     */
    bucket: string,
    /**
     * Durable capacity reconciliation identifier.
     */
    reconciliation: string,
  }): CancelablePromise<ObjectCapacityReconciliation | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/capacity-reconciliations/{reconciliation}',
      path: {
        'slug': slug,
        'bucket': bucket,
        'reconciliation': reconciliation,
      },
    });
  }
  /**
   * Cancel capacity reconciliation
   * Requires storage write scope and the bucket write grant. Immediately releases the active job's write pause without changing reserved capacity. Returns the job, including an existing terminal outcome. Cleanup remains available with storage disabled or spent budgets.
   * @returns ObjectCapacityReconciliation Cancelled or previously terminal reconciliation; Cache-Control no-store
   * @returns Problem Reconciliation missing or cancellation denied
   * @throws ApiError
   */
  public static cancelObjectCapacityReconciliation({
    slug,
    bucket,
    reconciliation,
  }: {
    /**
     * App owning the reconciliation job.
     */
    slug: string,
    /**
     * Bucket associated with the reconciliation.
     */
    bucket: string,
    /**
     * Durable capacity reconciliation identifier.
     */
    reconciliation: string,
  }): CancelablePromise<ObjectCapacityReconciliation | Problem> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/buckets/{bucket}/capacity-reconciliations/{reconciliation}',
      path: {
        'slug': slug,
        'bucket': bucket,
        'reconciliation': reconciliation,
      },
    });
  }
  /**
   * List tracked write receipts for a bucket
   * Requires storage write scope and the bucket write grant. Defaults to pending writes, newest first; pages are live views and receipts settling between reads may disappear from the pending filter. Reads remain available while storage is disabled or budgets are spent, without provider calls or quota admission. Direct signed uploads, legacy writes and multipart sessions are outside this receipt list.
   * @returns ObjectWriteReceiptList One page of tracked write receipts; Cache-Control no-store
   * @returns Problem Invalid pagination, missing bucket, or access denied
   * @throws ApiError
   */
  public static listObjectWriteReceipts({
    slug,
    bucket,
    status = 'pending',
    limit = 50,
    cursor,
  }: {
    /**
     * App owning the bucket.
     */
    slug: string,
    /**
     * Gregale bucket identifier.
     */
    bucket: string,
    /**
     * Receipt status to include.
     */
    status?: 'pending' | 'completed' | 'failed' | 'all',
    /**
     * Maximum receipts per page.
     */
    limit?: number,
    /**
     * Opaque next cursor from a page using the same bucket and status filter.
     */
    cursor?: string,
  }): CancelablePromise<ObjectWriteReceiptList | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/write-receipts',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
      query: {
        'status': status,
        'limit': limit,
        'cursor': cursor,
      },
    });
  }
  /**
   * Read a tracked write receipt
   * Requires storage write scope and the bucket write grant. Completed proves this attempt committed, not that the object still has this value. Pending has no confirmed outcome; do not assume failure or refund capacity. Reads remain available with storage disabled or spent budgets.
   * @returns ObjectWriteReceipt Receipt projection; Cache-Control no-store; pending responses include Retry-After
   * @returns Problem Receipt or bucket missing, or access denied
   * @throws ApiError
   */
  public static getObjectWriteReceipt({
    slug,
    bucket,
    receipt,
  }: {
    /**
     * App whose tracked write is being inspected.
     */
    slug: string,
    /**
     * Bucket containing the requested write receipt.
     */
    bucket: string,
    /**
     * Receipt ID returned as X-Gregale-Upload-ID.
     */
    receipt: string,
  }): CancelablePromise<ObjectWriteReceipt | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/write-receipts/{receipt}',
      path: {
        'slug': slug,
        'bucket': bucket,
        'receipt': receipt,
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
   * Issue a branded upload URL for one exact-length part
   * Requires storage:write or admin and a matching bucket grant. The URL
   * uses the branded S3 endpoint and binds the owned session, part and exact
   * byte length. It expires within 15 minutes and before the session deadline.
   * Upload without Gregale credentials and retain the ETag response header.
   * Current issuer grants and the active session are checked at dispatch.
   * Sequential retries may replace a settled part; overlapping or uncertain
   * native attempts remain fenced. Issuance and redemption consume the
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
