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
import type { ObjectBucketList } from '../models/ObjectBucketList.js';
import type { ObjectBucketNotifications } from '../models/ObjectBucketNotifications.js';
import type { ObjectBucketNotificationsRequest } from '../models/ObjectBucketNotificationsRequest.js';
import type { ObjectCapacityReconciliation } from '../models/ObjectCapacityReconciliation.js';
import type { ObjectDeletion } from '../models/ObjectDeletion.js';
import type { ObjectDeletionRequest } from '../models/ObjectDeletionRequest.js';
import type { ObjectMultipartPartList } from '../models/ObjectMultipartPartList.js';
import type { ObjectMultipartPartSignRequest } from '../models/ObjectMultipartPartSignRequest.js';
import type { ObjectMultipartUpload } from '../models/ObjectMultipartUpload.js';
import type { ObjectMultipartUploadList } from '../models/ObjectMultipartUploadList.js';
import type { ObjectS3CopySource } from '../models/ObjectS3CopySource.js';
import type { ObjectS3CopySourceList } from '../models/ObjectS3CopySourceList.js';
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
import type { ObjectWorkloadCredentialRequest } from '../models/ObjectWorkloadCredentialRequest.js';
import type { ObjectWorkloadCredentialResponse } from '../models/ObjectWorkloadCredentialResponse.js';
import type { Problem } from '../models/Problem.js';
import type { ReconcileObjectStorageComputeTriggersRequest } from '../models/ReconcileObjectStorageComputeTriggersRequest.js';
import type { SetObjectS3CopySourceRequest } from '../models/SetObjectS3CopySourceRequest.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
export class StorageService {
  protected static readonly clientConfig?: typeof OpenAPI;
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
      /**
       * Create the bucket with Object Lock. Permanent; the bucket keeps every object version. Some storage accepts Object Lock only when the bucket is created, so choose it here.
       */
      object_lock?: boolean;
    },
  }): CancelablePromise<ObjectBucket | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/buckets/{bucket}',
      path: {
        'slug': slug,
        'bucket': bucket,
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
   * List copy-only source grants
   * Requires storage:manage and destination write authority. Grants never permit ordinary source reads or listing with the destination S3 key. At most 32 source buckets may be granted per credential.
   * @returns ObjectS3CopySourceList Owned source grants without private grant identities
   * @returns Problem Copy source listing unavailable or access denied
   * @throws ApiError
   */
  public static listObjectS3CopySources({
    slug,
    bucket,
    credential,
  }: {
    /**
     * Destination app slug.
     */
    slug: string,
    /**
     * Destination bucket UUID.
     */
    bucket: string,
    /**
     * Owned destination writer credential UUID.
     */
    credential: string,
  }): CancelablePromise<ObjectS3CopySourceList | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/buckets/{bucket}/s3-credentials/{credential}/copy-sources',
      path: {
        'slug': slug,
        'bucket': bucket,
        'credential': credential,
      },
    });
  }
  /**
   * Grant an owned bucket as a copy source
   * Requires storage:manage, destination write and source read authority. Both buckets must be ready on the same native S3 placement. The prefix is literal and at most 1024 UTF-8 bytes. Identical retries preserve authority; changing or recreating a grant invalidates prepared copies. Signed URL credentials and rotation stages cannot acquire grants. CopySource uses the source bucket UUID followed by the encoded object key.
   * @returns ObjectS3CopySource Persisted copy source grant
   * @returns Problem Copy source grant invalid, unsupported, at limit, or denied
   * @throws ApiError
   */
  public static setObjectS3CopySource({
    slug,
    bucket,
    credential,
    source,
    requestBody,
  }: {
    /**
     * App containing the destination bucket.
     */
    slug: string,
    /**
     * Bucket receiving copied objects.
     */
    bucket: string,
    /**
     * Destination S3 credential receiving source authority.
     */
    credential: string,
    /**
     * Owned source bucket UUID, including across apps.
     */
    source: string,
    requestBody: SetObjectS3CopySourceRequest,
  }): CancelablePromise<ObjectS3CopySource | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/buckets/{bucket}/s3-credentials/{credential}/copy-sources/{source}',
      path: {
        'slug': slug,
        'bucket': bucket,
        'credential': credential,
        'source': source,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Revoke a copy-only source grant
   * Requires storage:manage and destination write authority. Prevents new copy dispatch while preserving already dispatched completion and recovery. Cleanup remains available for revoked destination credentials and disabled S3 ingress.
   * @returns Problem Copy source revocation denied or grant missing
   * @throws ApiError
   */
  public static deleteObjectS3CopySource({
    slug,
    bucket,
    credential,
    source,
  }: {
    /**
     * App containing the destination bucket.
     */
    slug: string,
    /**
     * Bucket receiving copied objects.
     */
    bucket: string,
    /**
     * Destination S3 credential receiving source authority.
     */
    credential: string,
    /**
     * Owned source bucket UUID, including across apps.
     */
    source: string,
  }): CancelablePromise<Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/buckets/{bucket}/s3-credentials/{credential}/copy-sources/{source}',
      path: {
        'slug': slug,
        'bucket': bucket,
        'credential': credential,
        'source': source,
      },
    });
  }
  /**
   * List compute bindings for a bucket
   * Requires storage:read, storage:manage or admin. Secret values are never returned; only the sealed app-secret names are listed.
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
   * Requires storage:read or admin. Non-admin keys also require a read or read_write grant on this bucket. An environment branch's bucket lists as the branch reads it, its own objects over its parent bucket's, keys it deleted hidden; each parent object has from_parent.
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
      /**
       * In an environment branch's bucket, the object is read from the parent environment's bucket.
       */
      from_parent?: boolean;
    }>;
    next_cursor?: string;
  } | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
   * Reconcile managed ObjectCreated triggers for the bucket's owning app
   * Requires MFA, storage manage scope and bucket write access. The destination
   * is always this bucket's owning app in its region. expected_revision is
   * mandatory and stale revisions return 409, including unchanged requests.
   * Replaces only managed rules, preserving manual notification rules.
   * Explicit triggers: [] clears managed ownership. Manual ID collisions
   * return 409; overlapping filters return 400. Nonempty configuration
   * requires object storage enabled and an async-invocation eligible plan.
   * Accepted deliveries retain their original destination snapshots.
   *
   * @returns ObjectBucketNotifications Complete notification configuration including managed rule identities; Cache-Control no-store
   * @returns Problem Missing owned bucket, storage disabled, or invalid request
   * @throws ApiError
   */
  public static reconcileObjectStorageComputeTriggers({
    slug,
    bucket,
    requestBody,
  }: {
    /**
     * App owning the bucket and trigger destination.
     */
    slug: string,
    /**
     * Bucket whose managed triggers are reconciled.
     */
    bucket: string,
    requestBody: ReconcileObjectStorageComputeTriggersRequest,
  }): CancelablePromise<ObjectBucketNotifications | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/buckets/{bucket}/compute-triggers',
      path: {
        'slug': slug,
        'bucket': bucket,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        409: `code: conflict`,
      },
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
   * Requires storage manage scope and the bucket write grant. Atomically replaces notification rules and validates Gregale-owned function or queue ARNs in this account and region. Empty rules clear intent. Full replacement clears managed compute-trigger ownership, including identical rules. Accepted events retain captured destinations. Nonempty configuration requires storage ingress to be enabled.
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/buckets/{bucket}/notifications',
      path: {
        'slug': slug,
        'bucket': bucket,
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
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
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/account/object-storage-usage',
    });
  }
  /**
   * Trade a binding token for a one-hour S3 credential on a granted bucket.
   * Called by guest-init's loopback credential endpoint, not by customer
   * code. A live production instance presents its gregale:bindings
   * workload token and names a bucket its app's gregale.yaml `uses:`
   * grants (or omits the bucket when it holds exactly one bucket grant).
   * The answer is the AWS container credential shape, limited to the
   * grant's access and key prefix, and expires within the hour. Each
   * instance holds at most 8 live credentials per bucket. Gated by the
   * cross_app_bindings platform flag (off: 404).
   *
   * @returns ObjectWorkloadCredentialResponse The credential.
   * @throws ApiError
   */
  public static workloadObjectCredentialIssue({
    requestBody,
  }: {
    requestBody: ObjectWorkloadCredentialRequest,
  }): CancelablePromise<ObjectWorkloadCredentialResponse> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/workload/object-storage/credentials',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * List organization buckets and creation capabilities
   * Owners, admins, developers and viewers can inspect bucket metadata. Requires the corresponding storage scope and current organization membership. Mutations require organization billing adoption. storage:manage lists every bucket. storage:read/storage:write keys see only buckets with an explicit grant. Admin and dashboard sessions list every bucket.
   * @returns ObjectBucketList List organization buckets and creation capabilities. Buckets across this app's environment scopes; backend credentials are never exposed.
   * @returns Problem List organization buckets and creation capabilities. Authentication, authorization, or storage error
   * @throws ApiError
   */
  public static listOrgObjectBuckets({
    organization,
    slug,
  }: {
    /**
     * Company bucket catalog: Organization owning this application and its storage catalog.
     */
    organization: string,
    /**
     * Company bucket catalog: App whose bucket catalog is being managed.
     */
    slug: string,
  }): CancelablePromise<ObjectBucketList | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/apps/{slug}/buckets',
      path: {
        'organization': organization,
        'slug': slug,
      },
    });
  }
  /**
   * Create a bucket for an organization application
   * Owners and admins manage bucket credentials, access grants and bucket lifecycle. Requires the corresponding storage scope and current organization membership. Mutations require organization billing adoption. Requires storage:manage or admin. Idempotent by app, scope and name, not
   * by Idempotency-Key. Retry provisioning by submitting the same name and
   * scope. Existing buckets retain their backend when the default changes.
   *
   * @returns ObjectBucket Create a bucket for an organization application. Existing ready bucket
   * @returns Problem Create a bucket for an organization application. Invalid request, access denied, bucket limit/conflict, or provider unavailable
   * @throws ApiError
   */
  public static createOrgObjectBucket({
    organization,
    slug,
    requestBody,
  }: {
    /**
     * Company bucket catalog: Organization owning this application and its storage catalog.
     */
    organization: string,
    /**
     * Company bucket catalog: App whose bucket catalog is being managed.
     */
    slug: string,
    requestBody: {
      name: string;
      scope?: string;
      /**
       * Create a bucket for an organization application. Gregale region, not upstream signing region. Omit to use the configured default.
       */
      region?: string;
      /**
       * Create a bucket for an organization application. Serve objects anonymously on the app hostname.
       */
      public?: boolean;
      /**
       * Create a bucket for an organization application. Immutable app path mounted when public is true.
       */
      serve_at?: string;
      /**
       * Create a bucket for an organization application. Create the bucket with Object Lock. Permanent; the bucket keeps every object version. Some storage accepts Object Lock only when the bucket is created, so choose it here.
       */
      object_lock?: boolean;
    },
  }): CancelablePromise<ObjectBucket | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/apps/{slug}/buckets',
      path: {
        'organization': organization,
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Delete an empty organization bucket
   * Owners and admins manage bucket credentials, access grants and bucket lifecycle. Requires the corresponding storage scope and current organization membership. Mutations require organization billing adoption. Requires storage:manage or admin. Never recursively deletes data. Nonempty buckets return 409. Repeat after successful deletion returns 404.
   * @returns Problem Delete an empty organization bucket. Access denied, bucket missing/nonempty/busy, or provider unavailable
   * @throws ApiError
   */
  public static deleteOrgObjectBucket({
    organization,
    slug,
    bucket,
  }: {
    /**
     * Company /{bucket}: Organization owning this application and its storage catalog.
     */
    organization: string,
    /**
     * Company /{bucket}: App that owns the bucket to remove.
     */
    slug: string,
    /**
     * Company /{bucket}: Identifier of the empty bucket to delete.
     */
    bucket: string,
  }): CancelablePromise<Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/orgs/{organization}/apps/{slug}/buckets/{bucket}',
      path: {
        'organization': organization,
        'slug': slug,
        'bucket': bucket,
      },
    });
  }
  /**
   * List organization user S3 credentials
   * Owners and admins manage bucket credentials, access grants and bucket lifecycle. Requires the corresponding storage scope and current organization membership. Mutations require organization billing adoption. Requires storage:manage or admin. Secret access keys are never returned by this endpoint.
   * @returns ObjectS3CredentialList List organization user S3 credentials. Active bucket-scoped S3 credentials; Cache-Control no-store
   * @returns Problem List organization user S3 credentials. S3 credential listing denied or its bucket is unavailable
   * @throws ApiError
   */
  public static listOrgObjectS3Credentials({
    organization,
    slug,
    bucket,
  }: {
    /**
     * Company /{bucket}/s3-credentials: Organization owning this application and its storage catalog.
     */
    organization: string,
    /**
     * Company /{bucket}/s3-credentials: App whose new S3 credential will be bucket-scoped.
     */
    slug: string,
    /**
     * Company /{bucket}/s3-credentials: Bucket receiving the S3 credential.
     */
    bucket: string,
  }): CancelablePromise<ObjectS3CredentialList | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/s3-credentials',
      path: {
        'organization': organization,
        'slug': slug,
        'bucket': bucket,
      },
    });
  }
  /**
   * Create a user S3 key for an organization bucket
   * Owners and admins manage bucket credentials, access grants and bucket lifecycle. Requires the corresponding storage scope and current organization membership. Mutations require organization billing adoption. The secret access key is returned exactly once, sealed at rest, and never recoverable through the control-plane API. At most ten active credentials may exist per bucket.
   * @returns Problem Create a user S3 key for an organization bucket. Invalid request, credential limit, unavailable sealing key, or access denied
   * @returns ObjectS3CredentialSecret Create a user S3 key for an organization bucket. One-time S3 credential response; Cache-Control no-store
   * @throws ApiError
   */
  public static createOrgObjectS3Credential({
    organization,
    slug,
    bucket,
    requestBody,
  }: {
    /**
     * Company /{bucket}/s3-credentials: Organization owning this application and its storage catalog.
     */
    organization: string,
    /**
     * Company /{bucket}/s3-credentials: App whose new S3 credential will be bucket-scoped.
     */
    slug: string,
    /**
     * Company /{bucket}/s3-credentials: Bucket receiving the S3 credential.
     */
    bucket: string,
    requestBody: CreateObjectS3CredentialRequest,
  }): CancelablePromise<Problem | ObjectS3CredentialSecret> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/s3-credentials',
      path: {
        'organization': organization,
        'slug': slug,
        'bucket': bucket,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Revoke an organization user S3 key
   * Owners and admins manage bucket credentials, access grants and bucket lifecycle. Requires the corresponding storage scope and current organization membership. Mutations require organization billing adoption. Revocation is immediate for new requests at s3.gregale.dev. Existing provider-signed internal requests are never exposed to the customer.
   * @returns Problem Revoke an organization user S3 key. Credential or bucket missing, or access denied
   * @throws ApiError
   */
  public static revokeOrgObjectS3Credential({
    organization,
    slug,
    bucket,
    credential,
  }: {
    /**
     * Company /{bucket}/s3-credentials/{credential}: Organization owning this application and its storage catalog.
     */
    organization: string,
    /**
     * Company /{bucket}/s3-credentials/{credential}: App whose S3 credential is being revoked.
     */
    slug: string,
    /**
     * Company /{bucket}/s3-credentials/{credential}: Bucket whose credential is being revoked.
     */
    bucket: string,
    /**
     * Company /{bucket}/s3-credentials/{credential}: Gregale S3 credential identifier.
     */
    credential: string,
  }): CancelablePromise<Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/s3-credentials/{credential}',
      path: {
        'organization': organization,
        'slug': slug,
        'bucket': bucket,
        'credential': credential,
      },
    });
  }
  /**
   * List organization application bucket bindings
   * Owners, admins, developers and viewers can inspect application bindings. Requires storage:manage or admin and current organization membership. Secret values are never returned; only the sealed app-secret names are listed.
   * @returns ObjectStorageComputeBindingList List organization application bucket bindings. Active compute bindings; Cache-Control no-store
   * @returns Problem List organization application bucket bindings. Binding listing denied or its bucket is unavailable
   * @throws ApiError
   */
  public static listOrgObjectStorageComputeBindings({
    organization,
    slug,
    bucket,
  }: {
    /**
     * Company /{bucket}/compute-bindings: Organization owning this application and its storage catalog.
     */
    organization: string,
    /**
     * Company /{bucket}/compute-bindings: App receiving the sealed storage connection settings.
     */
    slug: string,
    /**
     * Company /{bucket}/compute-bindings: Bucket exposed to the compute workload.
     */
    bucket: string,
  }): CancelablePromise<ObjectStorageComputeBindingList | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/compute-bindings',
      path: {
        'organization': organization,
        'slug': slug,
        'bucket': bucket,
      },
    });
  }
  /**
   * Bind an organization bucket to its application environment
   * Owners, admins and developers can manage application bindings. Requires the corresponding storage scope and current organization membership. Mutations require organization billing adoption. Creates a bucket-scoped Gregale S3 credential and injects endpoint, region, bucket, access-key, secret-key, and addressing settings as sealed app secrets. The secret values are never returned.
   * @returns Problem Bind an organization bucket to its application environment. Invalid request, secret quota/conflict, unavailable sealing key, or access denied
   * @returns ObjectStorageComputeBinding Bind an organization bucket to its application environment. Compute binding created; Cache-Control no-store
   * @throws ApiError
   */
  public static createOrgObjectStorageComputeBinding({
    organization,
    slug,
    bucket,
    requestBody,
  }: {
    /**
     * Company /{bucket}/compute-bindings: Organization owning this application and its storage catalog.
     */
    organization: string,
    /**
     * Company /{bucket}/compute-bindings: App receiving the sealed storage connection settings.
     */
    slug: string,
    /**
     * Company /{bucket}/compute-bindings: Bucket exposed to the compute workload.
     */
    bucket: string,
    requestBody: CreateObjectStorageComputeBindingRequest,
  }): CancelablePromise<Problem | ObjectStorageComputeBinding> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/compute-bindings',
      path: {
        'organization': organization,
        'slug': slug,
        'bucket': bucket,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Revoke an organization application bucket binding
   * Owners, admins and developers can manage application bindings. Requires the corresponding storage scope and current organization membership. Mutations require organization billing adoption. Revokes the bucket-scoped S3 credential before removing its sealed app secrets. The operation is idempotent after revocation.
   * @returns Problem Revoke an organization application bucket binding. Binding or bucket missing, or access denied
   * @throws ApiError
   */
  public static deleteOrgObjectStorageComputeBinding({
    organization,
    slug,
    bucket,
    binding,
  }: {
    /**
     * Company /{bucket}/compute-bindings/{binding}: Organization owning this application and its storage catalog.
     */
    organization: string,
    /**
     * Company /{bucket}/compute-bindings/{binding}: App owning the compute binding.
     */
    slug: string,
    /**
     * Company /{bucket}/compute-bindings/{binding}: Bucket attached to the compute binding.
     */
    bucket: string,
    /**
     * Company /{bucket}/compute-bindings/{binding}: Opaque compute binding identifier.
     */
    binding: string,
  }): CancelablePromise<Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'DELETE',
      url: '/v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/compute-bindings/{binding}',
      path: {
        'organization': organization,
        'slug': slug,
        'bucket': bucket,
        'binding': binding,
      },
    });
  }
  /**
   * Rotate an organization application bucket binding
   * Owners and admins can rotate application bindings. Requires the corresponding storage scope and current organization membership. Mutations require organization billing adoption. Atomically replaces the bucket-scoped access key and two sealed app secrets while keeping the binding and environment variable names stable. For a live app, the previous key remains valid until the rolling runtime refresh drains old instances; rotation_pending is true in the response. Retrying during a pending rotation requeues the same refresh. Secret values are never returned.
   * @returns ObjectStorageComputeBinding Rotate an organization application bucket binding. Binding rotated; Cache-Control no-store
   * @returns Problem Rotate an organization application bucket binding. Binding unavailable, sealing key unavailable, or access denied
   * @throws ApiError
   */
  public static rotateOrgObjectStorageComputeBinding({
    organization,
    slug,
    bucket,
    binding,
  }: {
    /**
     * Company /{bucket}/compute-bindings/{binding}/rotate: Organization owning this application and its storage catalog.
     */
    organization: string,
    /**
     * Company /{bucket}/compute-bindings/{binding}/rotate: App whose workload receives the rotated credential.
     */
    slug: string,
    /**
     * Company /{bucket}/compute-bindings/{binding}/rotate: Logical bucket whose access key is being rotated.
     */
    bucket: string,
    /**
     * Company /{bucket}/compute-bindings/{binding}/rotate: Binding identifier retained across credential rotations.
     */
    binding: string,
  }): CancelablePromise<ObjectStorageComputeBinding | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/compute-bindings/{binding}/rotate',
      path: {
        'organization': organization,
        'slug': slug,
        'bucket': bucket,
        'binding': binding,
      },
    });
  }
  /**
   * List objects in an organization bucket
   * Owners, admins, developers and viewers can inspect bucket metadata. Requires the corresponding storage scope and current organization membership. Mutations require organization billing adoption. Requires storage:read or admin. Non-admin keys also require a read or read_write grant on this bucket.
   * @returns any List objects in an organization bucket. One page, not a bucket-wide usage or billing total
   * @returns Problem List objects in an organization bucket. Invalid request, access denied, bucket unavailable, or provider error
   * @throws ApiError
   */
  public static listOrgBucketObjects({
    organization,
    slug,
    bucket,
    prefix,
    cursor,
    limit = 100,
  }: {
    /**
     * Company /{bucket}/objects: Organization owning this application and its storage catalog.
     */
    organization: string,
    /**
     * Company /{bucket}/objects: App containing the objects being managed.
     */
    slug: string,
    /**
     * Company /{bucket}/objects: Identifier of the bucket whose objects are being managed.
     */
    bucket: string,
    /**
     * List objects in an organization bucket. Only return keys starting with this prefix.
     */
    prefix?: string,
    /**
     * List objects in an organization bucket. Opaque next_cursor from the preceding page.
     */
    cursor?: string,
    /**
     * List objects in an organization bucket. Maximum objects in this page.
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
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/apps/{slug}/buckets/{bucket}/objects',
      path: {
        'organization': organization,
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
   * Read aggregate organization object storage usage
   * Owners, admins, developers, viewers and billing seats can read usage with usage:read or admin. Requires billing adoption. Aggregates company buckets across their retained storage namespaces; excludes personal buckets. Reservations are capacity commitments. Fresh false blocks further admission until allocated provider evidence arrives.
   * @returns ObjectStorageUsageResponse Read aggregate organization object storage usage. Organization storage accounting: Current account usage; Cache-Control no-store
   * @returns Problem Read aggregate organization object storage usage. Organization storage accounting: Access denied or accounting unavailable
   * @throws ApiError
   */
  public static getOrgObjectStorageUsage({
    organization,
  }: {
    /**
     * Company whose aggregate object storage accounting is requested.
     */
    organization: string,
  }): CancelablePromise<ObjectStorageUsageResponse | Problem> {
    return __request(this?.clientConfig ?? OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{organization}/object-storage-usage',
      path: {
        'organization': organization,
      },
    });
  }
}
