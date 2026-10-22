/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateManagedRealtimeEndpointRequest } from '../models/CreateManagedRealtimeEndpointRequest.js';
import type { FinalizeManagedRealtimeAuthResponse } from '../models/FinalizeManagedRealtimeAuthResponse.js';
import type { ManagedRealtimeAccessRequest } from '../models/ManagedRealtimeAccessRequest.js';
import type { ManagedRealtimeAccessResponse } from '../models/ManagedRealtimeAccessResponse.js';
import type { ManagedRealtimeCloseRequest } from '../models/ManagedRealtimeCloseRequest.js';
import type { ManagedRealtimeConnectionListResponse } from '../models/ManagedRealtimeConnectionListResponse.js';
import type { ManagedRealtimeDrainRequest } from '../models/ManagedRealtimeDrainRequest.js';
import type { ManagedRealtimeDrainResponse } from '../models/ManagedRealtimeDrainResponse.js';
import type { ManagedRealtimeEndpointResponse } from '../models/ManagedRealtimeEndpointResponse.js';
import type { ManagedRealtimeHistoryUsageResponse } from '../models/ManagedRealtimeHistoryUsageResponse.js';
import type { ManagedRealtimeInboxResponse } from '../models/ManagedRealtimeInboxResponse.js';
import type { ManagedRealtimeMessageMutationRequest } from '../models/ManagedRealtimeMessageMutationRequest.js';
import type { ManagedRealtimeMessageMutationResponse } from '../models/ManagedRealtimeMessageMutationResponse.js';
import type { ManagedRealtimeMessageRequest } from '../models/ManagedRealtimeMessageRequest.js';
import type { ManagedRealtimeOccupancyResponse } from '../models/ManagedRealtimeOccupancyResponse.js';
import type { ManagedRealtimePrincipalMessageRequest } from '../models/ManagedRealtimePrincipalMessageRequest.js';
import type { ManagedRealtimePrincipalReceiptResponse } from '../models/ManagedRealtimePrincipalReceiptResponse.js';
import type { ManagedRealtimePrincipalSendResponse } from '../models/ManagedRealtimePrincipalSendResponse.js';
import type { ManagedRealtimePublishResponse } from '../models/ManagedRealtimePublishResponse.js';
import type { ManagedRealtimePushDelivery } from '../models/ManagedRealtimePushDelivery.js';
import type { ManagedRealtimePushDevice } from '../models/ManagedRealtimePushDevice.js';
import type { ManagedRealtimePushProvider } from '../models/ManagedRealtimePushProvider.js';
import type { ManagedRealtimePushProviderRequest } from '../models/ManagedRealtimePushProviderRequest.js';
import type { ManagedRealtimePushRegistration } from '../models/ManagedRealtimePushRegistration.js';
import type { ManagedRealtimeReadProgressRequest } from '../models/ManagedRealtimeReadProgressRequest.js';
import type { ManagedRealtimeReadProgressResponse } from '../models/ManagedRealtimeReadProgressResponse.js';
import type { ManagedRealtimeRetainedHistoryResponse } from '../models/ManagedRealtimeRetainedHistoryResponse.js';
import type { ManagedRealtimeRetainedMessageRequest } from '../models/ManagedRealtimeRetainedMessageRequest.js';
import type { ManagedRealtimeRetainedMessageResponse } from '../models/ManagedRealtimeRetainedMessageResponse.js';
import type { Problem } from '../models/Problem.js';
import type { RealtimeAcknowledgement } from '../models/RealtimeAcknowledgement.js';
import type { RealtimeConsumerGroup } from '../models/RealtimeConsumerGroup.js';
import type { RealtimeConsumerGroupBatch } from '../models/RealtimeConsumerGroupBatch.js';
import type { RealtimeConsumerGroupBatchRequest } from '../models/RealtimeConsumerGroupBatchRequest.js';
import type { RealtimeConsumerGroupBatchSettlementRequest } from '../models/RealtimeConsumerGroupBatchSettlementRequest.js';
import type { RealtimeConsumerGroupBatchSettlementResult } from '../models/RealtimeConsumerGroupBatchSettlementResult.js';
import type { RealtimeConsumerGroupCircuitPolicy } from '../models/RealtimeConsumerGroupCircuitPolicy.js';
import type { RealtimeConsumerGroupCircuitStatus } from '../models/RealtimeConsumerGroupCircuitStatus.js';
import type { RealtimeConsumerGroupClaim } from '../models/RealtimeConsumerGroupClaim.js';
import type { RealtimeConsumerGroupClaimRatePolicy } from '../models/RealtimeConsumerGroupClaimRatePolicy.js';
import type { RealtimeConsumerGroupClaimRateStatus } from '../models/RealtimeConsumerGroupClaimRateStatus.js';
import type { RealtimeConsumerGroupClaimRequest } from '../models/RealtimeConsumerGroupClaimRequest.js';
import type { RealtimeConsumerGroupCreateRequest } from '../models/RealtimeConsumerGroupCreateRequest.js';
import type { RealtimeConsumerGroupDeadLetter } from '../models/RealtimeConsumerGroupDeadLetter.js';
import type { RealtimeConsumerGroupDeliveryHistory } from '../models/RealtimeConsumerGroupDeliveryHistory.js';
import type { RealtimeConsumerGroupDrainState } from '../models/RealtimeConsumerGroupDrainState.js';
import type { RealtimeConsumerGroupLagAlert } from '../models/RealtimeConsumerGroupLagAlert.js';
import type { RealtimeConsumerGroupLagAlertConfig } from '../models/RealtimeConsumerGroupLagAlertConfig.js';
import type { RealtimeConsumerGroupMetrics } from '../models/RealtimeConsumerGroupMetrics.js';
import type { RealtimeConsumerGroupPauseState } from '../models/RealtimeConsumerGroupPauseState.js';
import type { RealtimeConsumerGroupPriorityStatus } from '../models/RealtimeConsumerGroupPriorityStatus.js';
import type { RealtimeConsumerGroupResyncRequest } from '../models/RealtimeConsumerGroupResyncRequest.js';
import type { RealtimeConsumerGroupRetentionPolicy } from '../models/RealtimeConsumerGroupRetentionPolicy.js';
import type { RealtimeConsumerGroupRetentionRequest } from '../models/RealtimeConsumerGroupRetentionRequest.js';
import type { RealtimeConsumerGroupRetryPolicyRequest } from '../models/RealtimeConsumerGroupRetryPolicyRequest.js';
import type { RealtimeConsumerGroupRetryStatus } from '../models/RealtimeConsumerGroupRetryStatus.js';
import type { RealtimeConsumerGroupSettlement } from '../models/RealtimeConsumerGroupSettlement.js';
import type { RealtimeConsumerGroupSettlementResult } from '../models/RealtimeConsumerGroupSettlementResult.js';
import type { RealtimeDeadLetterBulkReplayRequest } from '../models/RealtimeDeadLetterBulkReplayRequest.js';
import type { RealtimeDeadLetterBulkReplayResult } from '../models/RealtimeDeadLetterBulkReplayResult.js';
import type { RealtimeDeadLetterPreview } from '../models/RealtimeDeadLetterPreview.js';
import type { RealtimeDeadLetterPreviewRequest } from '../models/RealtimeDeadLetterPreviewRequest.js';
import type { RealtimeNotificationPreferences } from '../models/RealtimeNotificationPreferences.js';
import type { RealtimePublisherCredential } from '../models/RealtimePublisherCredential.js';
import type { RealtimePublisherCredentialIssued } from '../models/RealtimePublisherCredentialIssued.js';
import type { RealtimePublisherCredentialRequest } from '../models/RealtimePublisherCredentialRequest.js';
import type { RealtimePublisherMessageRequest } from '../models/RealtimePublisherMessageRequest.js';
import type { RealtimePublisherPublishResponse } from '../models/RealtimePublisherPublishResponse.js';
import type { RealtimeReplayJob } from '../models/RealtimeReplayJob.js';
import type { RealtimeReplayJobRequest } from '../models/RealtimeReplayJobRequest.js';
import type { RealtimeTicketRequest } from '../models/RealtimeTicketRequest.js';
import type { RealtimeTicketResponse } from '../models/RealtimeTicketResponse.js';
import type { RealtimeTicketsRevokeResponse } from '../models/RealtimeTicketsRevokeResponse.js';
import type { RealtimeWorkerBatchClaimRequest } from '../models/RealtimeWorkerBatchClaimRequest.js';
import type { RealtimeWorkerClaimRequest } from '../models/RealtimeWorkerClaimRequest.js';
import type { RealtimeWorkerCredential } from '../models/RealtimeWorkerCredential.js';
import type { RealtimeWorkerCredentialIssued } from '../models/RealtimeWorkerCredentialIssued.js';
import type { RealtimeWorkerCredentialRequest } from '../models/RealtimeWorkerCredentialRequest.js';
import type { RealtimeWorkerCredentialRotationRequest } from '../models/RealtimeWorkerCredentialRotationRequest.js';
import type { RealtimeWorkerHealth } from '../models/RealtimeWorkerHealth.js';
import type { RealtimeWorkerHeartbeat } from '../models/RealtimeWorkerHeartbeat.js';
import type { RealtimeWorkerHeartbeatRequest } from '../models/RealtimeWorkerHeartbeatRequest.js';
import type { RotateManagedRealtimeAuthRequest } from '../models/RotateManagedRealtimeAuthRequest.js';
import type { RotateManagedRealtimeAuthResponse } from '../models/RotateManagedRealtimeAuthResponse.js';
import type { UpdateManagedRealtimeEndpointRequest } from '../models/UpdateManagedRealtimeEndpointRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class RealtimeService {
  /**
   * Read publisher grant and usage metadata
   * Account/app-scoped publisher credential administration. Issuance/rotation/revocation require admin scope and MFA. Plaintext returned once; generic response caching is excluded. Revocation remains available when the endpoint or history preview is disabled.
   * @returns RealtimePublisherCredential Operation result
   * @throws ApiError
   */
  public static listRealtimePublisherCredentials({
    slug,
    id,
  }: {
    /**
     * Application slug for read publisher grant and usage metadata.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for read publisher grant and usage metadata.
     */
    id: string,
  }): CancelablePromise<Array<RealtimePublisherCredential>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        400: `Invalid payload or policy`,
        401: `Invalid/expired/revoked/rotated credential`,
        403: `Channel/event outside grant`,
        404: `Endpoint or credential unavailable`,
        409: `Idempotency content conflict`,
        429: `Publish rate or bounded receipt/credential capacity reached`,
      },
    });
  }
  /**
   * Issue a channel-scoped publisher credential
   * Issue a channel-scoped publisher credential. Account/app-scoped publisher credential administration. Issuance/rotation/revocation require admin scope and MFA. Plaintext returned once; generic response caching is excluded. Revocation remains available when the endpoint or history preview is disabled.
   * @returns RealtimePublisherCredentialIssued Issue a channel-scoped publisher credential. Operation result
   * @throws ApiError
   */
  public static issueRealtimePublisherCredential({
    slug,
    id,
    requestBody,
  }: {
    /**
     * Application slug for issue a channel-scoped publisher credential.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for issue a channel-scoped publisher credential.
     */
    id: string,
    requestBody: RealtimePublisherCredentialRequest,
  }): CancelablePromise<RealtimePublisherCredentialIssued> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Issue a channel-scoped publisher credential. Invalid payload or policy`,
        401: `Issue a channel-scoped publisher credential. Invalid/expired/revoked/rotated credential`,
        403: `Issue a channel-scoped publisher credential. Channel/event outside grant`,
        404: `Issue a channel-scoped publisher credential. Endpoint or credential unavailable`,
        409: `Issue a channel-scoped publisher credential. Idempotency content conflict`,
        429: `Issue a channel-scoped publisher credential. Publish rate or bounded receipt/credential capacity reached`,
      },
    });
  }
  /**
   * Rotate publisher secret while preserving scopes, quota and receipts
   * Rotate publisher secret while preserving scopes, quota and receipts. Account/app-scoped publisher credential administration. Issuance/rotation/revocation require admin scope and MFA. Plaintext returned once; generic response caching is excluded. Revocation remains available when the endpoint or history preview is disabled.
   * @returns RealtimePublisherCredentialIssued Rotate publisher secret while preserving scopes, quota and receipts. Operation result
   * @throws ApiError
   */
  public static rotateRealtimePublisherCredential({
    slug,
    id,
    credentialId,
    requestBody,
  }: {
    /**
     * Application slug for rotate publisher secret while preserving scopes, quota and receipts.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for rotate publisher secret while preserving scopes, quota and receipts.
     */
    id: string,
    /**
     * Credential ID for rotate publisher secret while preserving scopes, quota and receipts.
     */
    credentialId: string,
    requestBody: RealtimeWorkerCredentialRotationRequest,
  }): CancelablePromise<RealtimePublisherCredentialIssued> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials/{credential_id}/rotate',
      path: {
        'slug': slug,
        'id': id,
        'credential_id': credentialId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Rotate publisher secret while preserving scopes, quota and receipts. Invalid payload or policy`,
        401: `Rotate publisher secret while preserving scopes, quota and receipts. Invalid/expired/revoked/rotated credential`,
        403: `Rotate publisher secret while preserving scopes, quota and receipts. Channel/event outside grant`,
        404: `Rotate publisher secret while preserving scopes, quota and receipts. Endpoint or credential unavailable`,
        409: `Rotate publisher secret while preserving scopes, quota and receipts. Idempotency content conflict`,
        429: `Rotate publisher secret while preserving scopes, quota and receipts. Publish rate or bounded receipt/credential capacity reached`,
      },
    });
  }
  /**
   * Revoke a publisher credential
   * Revoke a publisher credential. Account/app-scoped publisher credential administration. Issuance/rotation/revocation require admin scope and MFA. Plaintext returned once; generic response caching is excluded. Revocation remains available when the endpoint or history preview is disabled.
   * @returns void
   * @throws ApiError
   */
  public static revokeRealtimePublisherCredential({
    slug,
    id,
    credentialId,
  }: {
    /**
     * Application slug for revoke a publisher credential.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for revoke a publisher credential.
     */
    id: string,
    /**
     * Credential ID for revoke a publisher credential.
     */
    credentialId: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials/{credential_id}/revoke',
      path: {
        'slug': slug,
        'id': id,
        'credential_id': credentialId,
      },
      errors: {
        400: `Revoke a publisher credential. Invalid payload or policy`,
        401: `Revoke a publisher credential. Invalid/expired/revoked/rotated credential`,
        403: `Revoke a publisher credential. Channel/event outside grant`,
        404: `Revoke a publisher credential. Endpoint or credential unavailable`,
        409: `Revoke a publisher credential. Idempotency content conflict`,
        429: `Revoke a publisher credential. Publish rate or bounded receipt/credential capacity reached`,
      },
    });
  }
  /**
   * Publish a retained channel event with a scoped credential
   * Accepts only an endpoint-bound grp_ credential. Retained publication only; exact channel/event grants, decoded payload size, fixed-minute accepted-publish quota and idempotency receipt commit atomically. Matching retries within 24 hours return the original sequence without re-emitting or consuming quota; rotated credentials retain their receipt namespace. Requires history preview and live realtime plan access.
   * @returns RealtimePublisherPublishResponse Publish a retained channel event with a scoped credential. Operation result
   * @throws ApiError
   */
  public static publishWithRealtimePublisherCredential({
    endpointId,
    channel,
    idempotencyKey,
    requestBody,
  }: {
    /**
     * Endpoint id for publish a retained channel event with a scoped credential.
     */
    endpointId: string,
    /**
     * Channel name for publish a retained channel event with a scoped credential.
     */
    channel: string,
    /**
     * Stable key reused for retries of this operation for publish a retained channel event with a scoped credential.
     */
    idempotencyKey: string,
    requestBody: RealtimePublisherMessageRequest,
  }): CancelablePromise<RealtimePublisherPublishResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/realtime/publishers/endpoints/{endpoint_id}/channels/{channel}/publish',
      path: {
        'endpoint_id': endpointId,
        'channel': channel,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Publish a retained channel event with a scoped credential. Invalid payload or policy`,
        401: `Publish a retained channel event with a scoped credential. Invalid/expired/revoked/rotated credential`,
        403: `Publish a retained channel event with a scoped credential. Channel/event outside grant`,
        404: `Publish a retained channel event with a scoped credential. Endpoint or credential unavailable`,
        409: `Publish a retained channel event with a scoped credential. Idempotency content conflict`,
        429: `Publish a retained channel event with a scoped credential. Publish rate or bounded receipt/credential capacity reached`,
      },
    });
  }
  /**
   * List worker credential metadata
   * Account/app-scoped credential administration. Issuance, rotation and revocation require admin scope and MFA. Plaintext secrets are excluded from generic idempotency response caching.
   * @returns RealtimeWorkerCredential List worker credential metadata. Operation result
   * @throws ApiError
   */
  public static listRealtimeWorkerCredentials({
    slug,
    id,
    channel,
    group,
  }: {
    /**
     * Application slug for list worker credential metadata.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for list worker credential metadata.
     */
    id: string,
    /**
     * Channel name for list worker credential metadata.
     */
    channel: string,
    /**
     * Consumer group name for list worker credential metadata.
     */
    group: string,
  }): CancelablePromise<Array<RealtimeWorkerCredential>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      errors: {
        401: `Credential invalid, expired, rotated or revoked`,
        403: `Credential lacks permission`,
        404: `Group or credential not found`,
        409: `Lease lost or group conflict`,
        429: `Admission or authentication limit reached`,
      },
    });
  }
  /**
   * Issue a credential bound to one consumer group
   * Issue a credential bound to one consumer group. Account/app-scoped credential administration. Issuance, rotation and revocation require admin scope and MFA. Plaintext secrets are excluded from generic idempotency response caching.
   * @returns RealtimeWorkerCredentialIssued Issue a credential bound to one consumer group. Operation result
   * @throws ApiError
   */
  public static issueRealtimeWorkerCredential({
    slug,
    id,
    channel,
    group,
    requestBody,
  }: {
    /**
     * Application slug for issue a credential bound to one consumer group.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for issue a credential bound to one consumer group.
     */
    id: string,
    /**
     * Channel name for issue a credential bound to one consumer group.
     */
    channel: string,
    /**
     * Consumer group name for issue a credential bound to one consumer group.
     */
    group: string,
    requestBody: RealtimeWorkerCredentialRequest,
  }): CancelablePromise<RealtimeWorkerCredentialIssued> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `Issue a credential bound to one consumer group. Credential invalid, expired, rotated or revoked`,
        403: `Issue a credential bound to one consumer group. Credential lacks permission`,
        404: `Issue a credential bound to one consumer group. Group or credential not found`,
        409: `Issue a credential bound to one consumer group. Lease lost or group conflict`,
        429: `Issue a credential bound to one consumer group. Admission or authentication limit reached`,
      },
    });
  }
  /**
   * Rotate a worker credential and immediately invalidate the old secret
   * Rotate a worker credential and immediately invalidate the old secret. Account/app-scoped credential administration. Issuance, rotation and revocation require admin scope and MFA. Plaintext secrets are excluded from generic idempotency response caching.
   * @returns RealtimeWorkerCredentialIssued Rotate a worker credential and immediately invalidate the old secret. Operation result
   * @throws ApiError
   */
  public static rotateRealtimeWorkerCredential({
    slug,
    id,
    channel,
    group,
    credentialId,
    requestBody,
  }: {
    /**
     * Application slug for rotate a worker credential and immediately invalidate the old secret.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for rotate a worker credential and immediately invalidate the old secret.
     */
    id: string,
    /**
     * Channel name for rotate a worker credential and immediately invalidate the old secret.
     */
    channel: string,
    /**
     * Consumer group name for rotate a worker credential and immediately invalidate the old secret.
     */
    group: string,
    /**
     * Credential ID for rotate a worker credential and immediately invalidate the old secret.
     */
    credentialId: string,
    requestBody: RealtimeWorkerCredentialRotationRequest,
  }): CancelablePromise<RealtimeWorkerCredentialIssued> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials/{credential_id}/rotate',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
        'credential_id': credentialId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `Rotate a worker credential and immediately invalidate the old secret. Credential invalid, expired, rotated or revoked`,
        403: `Rotate a worker credential and immediately invalidate the old secret. Credential lacks permission`,
        404: `Rotate a worker credential and immediately invalidate the old secret. Group or credential not found`,
        409: `Rotate a worker credential and immediately invalidate the old secret. Lease lost or group conflict`,
        429: `Rotate a worker credential and immediately invalidate the old secret. Admission or authentication limit reached`,
      },
    });
  }
  /**
   * Revoke a worker credential
   * Revocation is available even when the endpoint or history preview is disabled. Once committed, later operations fail; already-authorized transactions can finish before revocation obtains the group lock. Revoked credentials cannot be rotated.
   * @returns void
   * @throws ApiError
   */
  public static revokeRealtimeWorkerCredential({
    slug,
    id,
    channel,
    group,
    credentialId,
  }: {
    /**
     * Application slug for revoke a worker credential.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for revoke a worker credential.
     */
    id: string,
    /**
     * Channel name for revoke a worker credential.
     */
    channel: string,
    /**
     * Consumer group name for revoke a worker credential.
     */
    group: string,
    /**
     * Credential ID for revoke a worker credential.
     */
    credentialId: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials/{credential_id}/revoke',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
        'credential_id': credentialId,
      },
      errors: {
        401: `Revoke a worker credential. Credential invalid, expired, rotated or revoked`,
        403: `Revoke a worker credential. Credential lacks permission`,
        404: `Revoke a worker credential. Group or credential not found`,
        409: `Revoke a worker credential. Lease lost or group conflict`,
        429: `Revoke a worker credential. Admission or authentication limit reached`,
      },
    });
  }
  /**
   * Read status using the status permission
   * Accepts only a group-bound grw_ worker credential. Exact permission is checked in the group transaction; group recreation does not restore access.
   * @returns RealtimeConsumerGroup Read status using the status permission. Operation result
   * @throws ApiError
   */
  public static getRealtimeWorkerGroup({
    groupId,
  }: {
    /**
     * Consumer group ID for read status using the status permission.
     */
    groupId: string,
  }): CancelablePromise<RealtimeConsumerGroup> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/realtime/workers/groups/{group_id}',
      path: {
        'group_id': groupId,
      },
      errors: {
        401: `Read status using the status permission. Credential invalid, expired, rotated or revoked`,
        403: `Read status using the status permission. Credential lacks permission`,
        404: `Read status using the status permission. Group or credential not found`,
        409: `Read status using the status permission. Lease lost or group conflict`,
        429: `Read status using the status permission. Admission or authentication limit reached`,
      },
    });
  }
  /**
   * Delete group using the separate delete permission
   * Delete group using the separate delete permission. Accepts only a group-bound grw_ worker credential. Exact permission is checked in the group transaction; group recreation does not restore access.
   * @returns void
   * @throws ApiError
   */
  public static deleteRealtimeWorkerGroup({
    groupId,
  }: {
    /**
     * Consumer group ID for delete group using the separate delete permission.
     */
    groupId: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/realtime/workers/groups/{group_id}',
      path: {
        'group_id': groupId,
      },
      errors: {
        401: `Delete group using the separate delete permission. Credential invalid, expired, rotated or revoked`,
        403: `Delete group using the separate delete permission. Credential lacks permission`,
        404: `Delete group using the separate delete permission. Group or credential not found`,
        409: `Delete group using the separate delete permission. Lease lost or group conflict`,
        429: `Delete group using the separate delete permission. Admission or authentication limit reached`,
      },
    });
  }
  /**
   * Claim a bounded batch with independent leases
   * Claim 1–16 messages in one transaction, bounded by the group-wide 32 active leases and 128 outstanding snapshots. Each message has its own token and settlement. Always returns 200 with a possibly empty claims array. Partial batches may report history_unavailable or capacity_blocked; process returned claims before resolving the blocker. Empty claims can mean caught up, delayed retries or full lease slots. Requires the existing claim permission. Tokens are returned once; no response caching or automatic claim retries.
   * @returns RealtimeConsumerGroupBatch Committed batch and optional blockers
   * @throws ApiError
   */
  public static claimRealtimeConsumerGroupBatch({
    slug,
    id,
    channel,
    group,
    requestBody,
  }: {
    /**
     * Application slug for claim a bounded batch with independent leases.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for claim a bounded batch with independent leases.
     */
    id: string,
    /**
     * Channel name for claim a bounded batch with independent leases.
     */
    channel: string,
    /**
     * Consumer group name for claim a bounded batch with independent leases.
     */
    group: string,
    requestBody: RealtimeConsumerGroupBatchRequest,
  }): CancelablePromise<RealtimeConsumerGroupBatch> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims/batch',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid worker or limit`,
        404: `Endpoint or group not found`,
        409: `Group conflict, lost claim lease, or retention gap`,
        429: `Admission capacity or event ID capacity reached; realtime_group_deduplication_full includes Retry-After and retry_after_seconds Group rate uses code realtime_group_rate_limited, Retry-After and retry_after_seconds.`,
      },
    });
  }
  /**
   * Claim a bounded batch with independent leases using scoped worker credentials
   * Claim a bounded batch with independent leases. Claim 1–16 messages in one transaction, bounded by the group-wide 32 active leases and 128 outstanding snapshots. Each message has its own token and settlement. Always returns 200 with a possibly empty claims array. Partial batches may report history_unavailable or capacity_blocked; process returned claims before resolving the blocker. Empty claims can mean caught up, delayed retries or full lease slots. Requires the existing claim permission. Tokens are returned once; no response caching or automatic claim retries.
   * @returns RealtimeConsumerGroupBatch Claim a bounded batch with independent leases. Committed batch and optional blockers
   * @throws ApiError
   */
  public static claimRealtimeWorkerGroupBatch({
    groupId,
    requestBody,
  }: {
    /**
     * Consumer group ID for claim a bounded batch with independent leases. Claim a bounded batch with independent leases. Claim a bounded batch with independent leases. Claim a bounded batch with independent leases.
     */
    groupId: string,
    requestBody: RealtimeWorkerBatchClaimRequest,
  }): CancelablePromise<RealtimeConsumerGroupBatch> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/realtime/workers/groups/{group_id}/claims/batch',
      path: {
        'group_id': groupId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Claim a bounded batch with independent leases. Invalid worker or limit`,
        401: `Claim a bounded batch with independent leases. Credential invalid, expired, rotated or revoked`,
        403: `Claim a bounded batch with independent leases. Credential lacks permission`,
        404: `Claim a bounded batch with independent leases. Group or credential not found`,
        409: `Claim a bounded batch with independent leases. Lease lost or group conflict`,
        429: `Claim a bounded batch with independent leases. Admission capacity or event ID capacity reached; realtime_group_deduplication_full includes Retry-After and retry_after_seconds Group rate uses code realtime_group_rate_limited, Retry-After and retry_after_seconds.`,
      },
    });
  }
  /**
   * Pause new consumer-group claims
   * Requires app ownership, admin scope and MFA. Idempotent group control. Existing leases may renew, complete or retry. Resume preserves cursor, attempt counts and retry delays. Pause does not stop lease clocks or channel retention. Returns paused state and nullable pause timestamp.
   * @returns RealtimeConsumerGroupPauseState Current pause state
   * @throws ApiError
   */
  public static pauseRealtimeConsumerGroup({
    slug,
    id,
    channel,
    group,
  }: {
    /**
     * Application slug for pause new consumer-group claims.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for pause new consumer-group claims.
     */
    id: string,
    /**
     * Channel name for pause new consumer-group claims.
     */
    channel: string,
    /**
     * Consumer group name for pause new consumer-group claims.
     */
    group: string,
  }): CancelablePromise<RealtimeConsumerGroupPauseState> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/pause',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      errors: {
        404: `Pause new consumer-group claims. Endpoint or group not found`,
        409: `Pause new consumer-group claims. Group conflict, lost claim lease, or retention gap`,
        429: `Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Resume new consumer-group claims
   * Resume new consumer-group claims. Requires app ownership, admin scope and MFA. Idempotent group control. Existing leases may renew, complete or retry. Resume preserves cursor, attempt counts and retry delays. Pause does not stop lease clocks or channel retention. Returns paused state and nullable pause timestamp.
   * @returns RealtimeConsumerGroupPauseState Resume new consumer-group claims. Current pause state
   * @throws ApiError
   */
  public static resumeRealtimeConsumerGroup({
    slug,
    id,
    channel,
    group,
  }: {
    /**
     * Application slug for resume new consumer-group claims.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for resume new consumer-group claims.
     */
    id: string,
    /**
     * Channel name for resume new consumer-group claims.
     */
    channel: string,
    /**
     * Consumer group name for resume new consumer-group claims.
     */
    group: string,
  }): CancelablePromise<RealtimeConsumerGroupPauseState> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/resume',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      errors: {
        404: `Resume new consumer-group claims. Endpoint or group not found`,
        409: `Resume new consumer-group claims. Group conflict, lost claim lease, or retention gap`,
        429: `Resume new consumer-group claims. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Pause new consumer-group claims using scoped worker credentials
   * Requires the explicit pause worker permission, rechecked in the group transaction. Idempotent group control. Existing leases may renew, complete or retry. Resume preserves cursor, attempt counts and retry delays. Pause does not stop lease clocks or channel retention. Returns paused state and nullable pause timestamp.
   * @returns RealtimeConsumerGroupPauseState Pause new consumer-group claims. Current pause state
   * @throws ApiError
   */
  public static pauseRealtimeWorkerGroup({
    groupId,
  }: {
    /**
     * Consumer group ID for pause new consumer-group claims. Pause new consumer-group claims. Pause new consumer-group claims. Pause new consumer-group claims.
     */
    groupId: string,
  }): CancelablePromise<RealtimeConsumerGroupPauseState> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/realtime/workers/groups/{group_id}/pause',
      path: {
        'group_id': groupId,
      },
      errors: {
        401: `Pause new consumer-group claims. Credential invalid, expired, rotated or revoked`,
        403: `Pause new consumer-group claims. Credential lacks permission`,
        404: `Pause new consumer-group claims. Group or credential not found`,
        409: `Pause new consumer-group claims. Lease lost or group conflict`,
        429: `Pause new consumer-group claims. Admission or authentication limit reached`,
      },
    });
  }
  /**
   * Resume new consumer-group claims using scoped worker credentials
   * Requires the explicit resume worker permission, rechecked in the group transaction. Idempotent group control. Existing leases may renew, complete or retry. Resume preserves cursor, attempt counts and retry delays. Pause does not stop lease clocks or channel retention. Returns paused state and nullable pause timestamp.
   * @returns RealtimeConsumerGroupPauseState The group pause state after resuming claims with the scoped worker credential.
   * @throws ApiError
   */
  public static resumeRealtimeWorkerGroup({
    groupId,
  }: {
    /**
     * Consumer group ID for resume new consumer-group claims. Resume new consumer-group claims. Resume new consumer-group claims. Resume new consumer-group claims.
     */
    groupId: string,
  }): CancelablePromise<RealtimeConsumerGroupPauseState> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/realtime/workers/groups/{group_id}/resume',
      path: {
        'group_id': groupId,
      },
      errors: {
        401: `Resume new consumer-group claims. Credential invalid, expired, rotated or revoked`,
        403: `Resume new consumer-group claims. Credential lacks permission`,
        404: `Resume new consumer-group claims. Group or credential not found`,
        409: `Resume new consumer-group claims. Lease lost or group conflict`,
        429: `Resume new consumer-group claims. Admission or authentication limit reached`,
      },
    });
  }
  /**
   * Drain active consumer-group leases
   * Idempotently stop new claims and enter persistent drain mode. Requires account admin scope and MFA, or the explicit drain worker permission. Existing leases may renew or settle. Drained means no unexpired leases remain; it does not prove handler side effects have stopped. Retry/dead/unassigned work is preserved. Resume explicitly after deployment; pause switches to ordinary paused mode.
   * @returns RealtimeConsumerGroupDrainState Drain active consumer-group leases. Current pause state
   * @throws ApiError
   */
  public static drainRealtimeConsumerGroup({
    slug,
    id,
    channel,
    group,
  }: {
    /**
     * Application slug for drain active consumer-group leases.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for drain active consumer-group leases.
     */
    id: string,
    /**
     * Channel name for drain active consumer-group leases.
     */
    channel: string,
    /**
     * Consumer group name for drain active consumer-group leases.
     */
    group: string,
  }): CancelablePromise<RealtimeConsumerGroupDrainState> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/drain',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      errors: {
        404: `Drain active consumer-group leases. Endpoint or group not found`,
        409: `Drain active consumer-group leases. Group conflict, lost claim lease, or retention gap`,
        429: `Drain active consumer-group leases. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Drain active consumer-group leases using scoped worker credentials
   * Drain active consumer-group leases. Idempotently stop new claims and enter persistent drain mode. Requires account admin scope and MFA, or the explicit drain worker permission. Existing leases may renew or settle. Drained means no unexpired leases remain; it does not prove handler side effects have stopped. Retry/dead/unassigned work is preserved. Resume explicitly after deployment; pause switches to ordinary paused mode.
   * @returns RealtimeConsumerGroupDrainState The drain state after the scoped worker stops new claims and waits for active leases.
   * @throws ApiError
   */
  public static drainRealtimeWorkerGroup({
    groupId,
  }: {
    /**
     * Consumer group ID for drain active consumer-group leases. Drain active consumer-group leases. Drain active consumer-group leases. Drain active consumer-group leases.
     */
    groupId: string,
  }): CancelablePromise<RealtimeConsumerGroupDrainState> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/realtime/workers/groups/{group_id}/drain',
      path: {
        'group_id': groupId,
      },
      errors: {
        401: `Drain active consumer-group leases. Credential invalid, expired, rotated or revoked`,
        403: `Drain active consumer-group leases. Credential lacks permission`,
        404: `Drain active consumer-group leases. Group or credential not found`,
        409: `Drain active consumer-group leases. Lease lost or group conflict`,
        429: `Drain active consumer-group leases. Admission or authentication limit reached`,
      },
    });
  }
  /**
   * Settle a batch of consumer group claims
   * Validate all 1 to 32 items before mutation; duplicate numeric sequences are invalid. Results preserve request order. Each valid current lease is completed, retried, released or renewed; expired, missing or differently owned leases return lease_lost without blocking other items. Scoped credentials require every action in the request, rechecked under the group lock; no additional grant. Account writes require ownership, deploy-write scope and MFA. Known lease failures commit alongside valid settlements; database errors roll back the batch. Optional idempotency_key saves the entire result for 24 hours; repeat the same request with the same key to recover a lost response. Different contents return 409 realtime_settlement_key_conflict. Without a key, a lost response may have committed.
   * @returns RealtimeConsumerGroupBatchSettlementResult Per-item settlement results
   * @throws ApiError
   */
  public static settleRealtimeConsumerGroupBatch({
    slug,
    id,
    channel,
    group,
    requestBody,
  }: {
    /**
     * Application slug for settle a batch of consumer group claims.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for settle a batch of consumer group claims.
     */
    id: string,
    /**
     * Channel name for settle a batch of consumer group claims.
     */
    channel: string,
    /**
     * Consumer group name for settle a batch of consumer group claims.
     */
    group: string,
    requestBody: RealtimeConsumerGroupBatchSettlementRequest,
  }): CancelablePromise<RealtimeConsumerGroupBatchSettlementResult> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims/settle/batch',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid or duplicate settlement`,
        401: `Invalid credentials`,
        403: `Action permission denied`,
        404: `Endpoint or group unavailable`,
        409: `Idempotency key reused with different contents (realtime_settlement_key_conflict)`,
        429: `Admission or authentication or settlement receipt capacity reached`,
      },
    });
  }
  /**
   * Settle a batch of consumer group claims using scoped worker credentials
   * Settle a batch of consumer group claims. Validate all 1 to 32 items before mutation; duplicate numeric sequences are invalid. Results preserve request order. Each valid current lease is completed, retried, released or renewed; expired, missing or differently owned leases return lease_lost without blocking other items. Scoped credentials require every action in the request, rechecked under the group lock; no additional grant. Account writes require ownership, deploy-write scope and MFA. Known lease failures commit alongside valid settlements; database errors roll back the batch. Optional idempotency_key saves the entire result for 24 hours; repeat the same request with the same key to recover a lost response. Different contents return 409 realtime_settlement_key_conflict. Without a key, a lost response may have committed.
   * @returns RealtimeConsumerGroupBatchSettlementResult Settle a batch of consumer group claims. Per-item settlement results
   * @throws ApiError
   */
  public static settleRealtimeWorkerGroupBatch({
    groupId,
    requestBody,
  }: {
    /**
     * Consumer group ID for settle a batch of consumer group claims. Settle a batch of consumer group claims. Settle a batch of consumer group claims. Settle a batch of consumer group claims.
     */
    groupId: string,
    requestBody: RealtimeConsumerGroupBatchSettlementRequest,
  }): CancelablePromise<RealtimeConsumerGroupBatchSettlementResult> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/realtime/workers/groups/{group_id}/claims/settle/batch',
      path: {
        'group_id': groupId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Settle a batch of consumer group claims. Invalid or duplicate settlement`,
        401: `Settle a batch of consumer group claims. Invalid credentials`,
        403: `Settle a batch of consumer group claims. Action permission denied`,
        404: `Settle a batch of consumer group claims. Endpoint or group unavailable`,
        409: `Settle a batch of consumer group claims. Idempotency key reused with different contents (realtime_settlement_key_conflict)`,
        429: `Settle a batch of consumer group claims. Admission or authentication or settlement receipt capacity reached`,
      },
    });
  }
  /**
   * Read a message's observed consumer-group delivery timeline
   * Account ownership, read scope and MFA required. Bounded 24-hour history, at most 1024 events per group shared across messages. No pre-migration backfill or renewals. Completions remain after snapshot deletion. Empty results do not distinguish unprocessed, completed before migration, filtered or pruned messages. group_pruned_events counts expired/capacity-pruned observations across the whole group, not solely this message. Reads prune expired history under the group lock. Events are ordered by ID; observation timestamps can lag actual lease/deadline expiry. No payload, lease/credential token, token hash or worker identity is returned.
   * @returns RealtimeConsumerGroupDeliveryHistory Ordered retained history, possibly empty
   * @throws ApiError
   */
  public static getRealtimeConsumerGroupDeliveryHistory({
    slug,
    id,
    channel,
    group,
    sequence,
    afterId = '0',
    limit = 32,
  }: {
    /**
     * Application slug for read a message's observed consumer-group delivery timeline.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for read a message's observed consumer-group delivery timeline.
     */
    id: string,
    /**
     * Channel name for read a message's observed consumer-group delivery timeline.
     */
    channel: string,
    /**
     * Consumer group name for read a message's observed consumer-group delivery timeline.
     */
    group: string,
    /**
     * Message sequence for read a message's observed consumer-group delivery timeline.
     */
    sequence: string,
    /**
     * After id for read a message's observed consumer-group delivery timeline.
     */
    afterId?: string,
    /**
     * Maximum number of records for read a message's observed consumer-group delivery timeline.
     */
    limit?: number,
  }): CancelablePromise<RealtimeConsumerGroupDeliveryHistory> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/messages/{sequence}/history',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
        'sequence': sequence,
      },
      query: {
        'after_id': afterId,
        'limit': limit,
      },
      errors: {
        400: `Invalid group, sequence, cursor or limit`,
        401: `Read a message's observed consumer-group delivery timeline. Invalid credentials`,
        403: `Permission denied`,
        404: `Read a message's observed consumer-group delivery timeline. Endpoint or group unavailable`,
      },
    });
  }
  /**
   * Read consumer group circuit breaker
   * Opt-in consecutive processing-failure breaker; default disabled, threshold 5 and cooldown 30 seconds. Reported retries/dead failures and lease expiries count; normal delivery-deadline expiry does not. Manual pause/drain retain precedence. Cooldown transitions on claims to one fenced probe at a time; only its success restores admission. Existing leases may settle. Identical policy writes preserve state; changed policy resets closed. Status is observational; reads do not advance cooldown. Account ownership and MFA required; reads need read scope, writes admin scope. Scoped worker claims already obey the gate with no new grant.
   * @returns RealtimeConsumerGroupCircuitStatus Current policy and circuit state
   * @throws ApiError
   */
  public static getRealtimeConsumerGroupCircuit({
    slug,
    id,
    channel,
    group,
  }: {
    /**
     * Application slug for read consumer group circuit breaker.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for read consumer group circuit breaker.
     */
    id: string,
    /**
     * Channel name for read consumer group circuit breaker.
     */
    channel: string,
    /**
     * Consumer group name for read consumer group circuit breaker.
     */
    group: string,
  }): CancelablePromise<RealtimeConsumerGroupCircuitStatus> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/circuit',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      errors: {
        400: `Invalid policy or group`,
        401: `Read consumer group circuit breaker. Invalid credentials`,
        403: `Read consumer group circuit breaker. Permission denied`,
        404: `Read consumer group circuit breaker. Endpoint or group unavailable`,
      },
    });
  }
  /**
   * Configure consumer group circuit breaker
   * Configure consumer group circuit breaker. Opt-in consecutive processing-failure breaker; default disabled, threshold 5 and cooldown 30 seconds. Reported retries/dead failures and lease expiries count; normal delivery-deadline expiry does not. Manual pause/drain retain precedence. Cooldown transitions on claims to one fenced probe at a time; only its success restores admission. Existing leases may settle. Identical policy writes preserve state; changed policy resets closed. Status is observational; reads do not advance cooldown. Account ownership and MFA required; reads need read scope, writes admin scope. Scoped worker claims already obey the gate with no new grant.
   * @returns RealtimeConsumerGroupCircuitStatus Configure consumer group circuit breaker. Current policy and circuit state
   * @throws ApiError
   */
  public static configureRealtimeConsumerGroupCircuit({
    slug,
    id,
    channel,
    group,
    requestBody,
  }: {
    /**
     * Application slug for configure consumer group circuit breaker.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for configure consumer group circuit breaker.
     */
    id: string,
    /**
     * Channel name for configure consumer group circuit breaker.
     */
    channel: string,
    /**
     * Consumer group name for configure consumer group circuit breaker.
     */
    group: string,
    requestBody: RealtimeConsumerGroupCircuitPolicy,
  }): CancelablePromise<RealtimeConsumerGroupCircuitStatus> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/circuit',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Configure consumer group circuit breaker. Invalid policy or group`,
        401: `Configure consumer group circuit breaker. Invalid credentials`,
        403: `Configure consumer group circuit breaker. Permission denied`,
        404: `Configure consumer group circuit breaker. Endpoint or group unavailable`,
      },
    });
  }
  /**
   * Read priority policy and materialized backlog
   * Account ownership, read scope and MFA required. Policy is immutable at group creation. Backlog covers retained outstanding snapshots only, including delayed and blocked ready rows and unmaintained expired leases. The next-claim hint is observational and can change before a claim.
   * @returns RealtimeConsumerGroupPriorityStatus Current priority policy and backlog
   * @throws ApiError
   */
  public static getRealtimeConsumerGroupPriority({
    slug,
    id,
    channel,
    group,
  }: {
    /**
     * Application slug for read priority policy and materialized backlog.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for read priority policy and materialized backlog.
     */
    id: string,
    /**
     * Channel name for read priority policy and materialized backlog.
     */
    channel: string,
    /**
     * Consumer group name for read priority policy and materialized backlog.
     */
    group: string,
  }): CancelablePromise<RealtimeConsumerGroupPriorityStatus> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/priority',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      errors: {
        400: `Read priority policy and materialized backlog. Invalid group`,
        401: `Read priority policy and materialized backlog. Invalid credentials`,
        403: `Read priority policy and materialized backlog. Permission denied`,
        404: `Read priority policy and materialized backlog. Endpoint or group unavailable`,
      },
    });
  }
  /**
   * Read consumer group processing trends
   * Account-owned read with MFA. Returns 60 UTC minute buckets including the partial current minute. Queue wait averages publication to first claim, excluding pre-migration attempted snapshots. Processing averages successful attempts with an observed lease start, includes renewals, and excludes previous failed attempts. Counts are decimal strings. Zero averages with zero samples mean no observations. Retries count leased-to-ready transitions including expiry; dead letters count entries into dead including deadlines. Replays can add further transitions. Events are bucketed when observed, not when an unobserved lease expired. Empty minutes are zero-filled. No per-message telemetry is retained.
   * @returns RealtimeConsumerGroupMetrics Recent processing metrics
   * @throws ApiError
   */
  public static getRealtimeConsumerGroupMetrics({
    slug,
    id,
    channel,
    group,
  }: {
    /**
     * Application slug for read consumer group processing trends.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for read consumer group processing trends.
     */
    id: string,
    /**
     * Channel name for read consumer group processing trends.
     */
    channel: string,
    /**
     * Consumer group name for read consumer group processing trends.
     */
    group: string,
  }): CancelablePromise<RealtimeConsumerGroupMetrics> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/metrics',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      errors: {
        400: `Read consumer group processing trends. Invalid group`,
        401: `Read consumer group processing trends. Invalid credentials`,
        403: `Read consumer group processing trends. Permission denied`,
        404: `Read consumer group processing trends. Endpoint or group unavailable`,
        429: `Admission capacity reached`,
      },
    });
  }
  /**
   * Read observed worker liveness
   * Heartbeat monitoring is opt-in. Fixed timeout 60 seconds; report every 15 seconds. Account routes require ownership and MFA, writes deploy-write scope. Scoped POST requires heartbeat permission and fixes worker identity to credential name. Readiness is self-reported, separate from liveness. No lease is renewed.
   * @returns RealtimeWorkerHealth Current worker health
   * @throws ApiError
   */
  public static getRealtimeConsumerGroupWorkerHealth({
    slug,
    id,
    channel,
    group,
  }: {
    /**
     * Application slug for read observed worker liveness.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for read observed worker liveness.
     */
    id: string,
    /**
     * Channel name for read observed worker liveness.
     */
    channel: string,
    /**
     * Consumer group name for read observed worker liveness.
     */
    group: string,
  }): CancelablePromise<RealtimeWorkerHealth> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/heartbeats',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      errors: {
        400: `Invalid worker, readiness or diagnostic code`,
        401: `Read observed worker liveness. Invalid credentials`,
        403: `Read observed worker liveness. Permission denied`,
        404: `Read observed worker liveness. Endpoint or group unavailable`,
        429: `Worker heartbeat tracking or credential admission capacity reached`,
      },
    });
  }
  /**
   * Report worker readiness and liveness
   * Report worker readiness and liveness. Heartbeat monitoring is opt-in. Fixed timeout 60 seconds; report every 15 seconds. Account routes require ownership and MFA, writes deploy-write scope. Scoped POST requires heartbeat permission and fixes worker identity to credential name. Readiness is self-reported, separate from liveness. No lease is renewed.
   * @returns RealtimeWorkerHeartbeat Report worker readiness and liveness. Current worker health
   * @throws ApiError
   */
  public static heartbeatRealtimeConsumerGroup({
    slug,
    id,
    channel,
    group,
    requestBody,
  }: {
    /**
     * Application slug for report worker readiness and liveness.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for report worker readiness and liveness.
     */
    id: string,
    /**
     * Channel name for report worker readiness and liveness.
     */
    channel: string,
    /**
     * Consumer group name for report worker readiness and liveness.
     */
    group: string,
    requestBody: RealtimeWorkerHeartbeatRequest,
  }): CancelablePromise<RealtimeWorkerHeartbeat> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/heartbeats',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Report worker readiness and liveness. Invalid worker, readiness or diagnostic code`,
        401: `Report worker readiness and liveness. Invalid credentials`,
        403: `Report worker readiness and liveness. Permission denied`,
        404: `Report worker readiness and liveness. Endpoint or group unavailable`,
        429: `Report worker readiness and liveness. Worker heartbeat tracking or credential admission capacity reached`,
      },
    });
  }
  /**
   * Report liveness using a scoped worker credential
   * Report liveness using a scoped worker credential. Heartbeat monitoring is opt-in. Fixed timeout 60 seconds; report every 15 seconds. Account routes require ownership and MFA, writes deploy-write scope. Scoped POST requires heartbeat permission and fixes worker identity to credential name. Readiness is self-reported, separate from liveness. No lease is renewed.
   * @returns RealtimeWorkerHeartbeat Report liveness using a scoped worker credential. Current worker health
   * @throws ApiError
   */
  public static heartbeatRealtimeWorkerGroup({
    groupId,
    requestBody,
  }: {
    /**
     * Consumer group ID for report liveness using a scoped worker credential.
     */
    groupId: string,
    requestBody: RealtimeWorkerHeartbeatRequest,
  }): CancelablePromise<RealtimeWorkerHeartbeat> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/realtime/workers/groups/{group_id}/heartbeats',
      path: {
        'group_id': groupId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Report liveness using a scoped worker credential. Invalid worker, readiness or diagnostic code`,
        401: `Report liveness using a scoped worker credential. Invalid credentials`,
        403: `Report liveness using a scoped worker credential. Permission denied`,
        404: `Report liveness using a scoped worker credential. Endpoint or group unavailable`,
        429: `Report liveness using a scoped worker credential. Worker heartbeat tracking or credential admission capacity reached`,
      },
    });
  }
  /**
   * Read shared consumer-group claim rate
   * Requires app ownership and MFA; writes require admin scope. Zero rate disables throttling. Otherwise configure 1 to 1000 claims per second and a burst of 1 to 32. Admission is shared across every worker and replica through persisted group-locked state. Renewals, settlements and replay do not consume rate credit. Identical writes preserve credit and counters; changed policies clamp existing debt to the new burst window. The observed retry hint is not a reservation; concurrent workers can consume the next allowance.
   * @returns RealtimeConsumerGroupClaimRateStatus Policy and current throttle observation
   * @throws ApiError
   */
  public static getRealtimeConsumerGroupClaimRate({
    slug,
    id,
    channel,
    group,
  }: {
    /**
     * Application slug for read shared consumer-group claim rate.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for read shared consumer-group claim rate.
     */
    id: string,
    /**
     * Channel name for read shared consumer-group claim rate.
     */
    channel: string,
    /**
     * Consumer group name for read shared consumer-group claim rate.
     */
    group: string,
  }): CancelablePromise<RealtimeConsumerGroupClaimRateStatus> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claim-rate',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      errors: {
        400: `Invalid rate or burst`,
        404: `Read shared consumer-group claim rate. Endpoint or group not found`,
        409: `Read shared consumer-group claim rate. Group conflict, lost claim lease, or retention gap`,
        429: `Read shared consumer-group claim rate. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Configure shared consumer-group claim rate
   * Configure shared consumer-group claim rate. Requires app ownership and MFA; writes require admin scope. Zero rate disables throttling. Otherwise configure 1 to 1000 claims per second and a burst of 1 to 32. Admission is shared across every worker and replica through persisted group-locked state. Renewals, settlements and replay do not consume rate credit. Identical writes preserve credit and counters; changed policies clamp existing debt to the new burst window. The observed retry hint is not a reservation; concurrent workers can consume the next allowance.
   * @returns RealtimeConsumerGroupClaimRateStatus Configure shared consumer-group claim rate. Policy and current throttle observation
   * @throws ApiError
   */
  public static configureRealtimeConsumerGroupClaimRate({
    slug,
    id,
    channel,
    group,
    requestBody,
  }: {
    /**
     * Application slug for configure shared consumer-group claim rate.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for configure shared consumer-group claim rate.
     */
    id: string,
    /**
     * Channel name for configure shared consumer-group claim rate.
     */
    channel: string,
    /**
     * Consumer group name for configure shared consumer-group claim rate.
     */
    group: string,
    requestBody: RealtimeConsumerGroupClaimRatePolicy,
  }): CancelablePromise<RealtimeConsumerGroupClaimRateStatus> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claim-rate',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Configure shared consumer-group claim rate. Invalid rate or burst`,
        404: `Configure shared consumer-group claim rate. Endpoint or group not found`,
        409: `Configure shared consumer-group claim rate. Group conflict, lost claim lease, or retention gap`,
        429: `Configure shared consumer-group claim rate. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Read consumer-group retry policy
   * Requires app ownership and MFA; writes require admin scope. Policies are disabled by default. Enabled policies apply capped exponential backoff to reported failures with omitted retry_after_seconds. Explicit delays, including zero, override backoff. Exact permanent failure codes dead-letter immediately even with an explicit delay. Jitter samples uniformly from half the capped delay through the capped delay, in integer seconds. Attempt budgets still apply. Changes affect future settlements, not already scheduled retries or expired leases. next_retry_at is the earliest delayed ready snapshot; ordering, pause and capacity can delay its claim further.
   * @returns RealtimeConsumerGroupRetryStatus Policy and earliest delayed retry observation
   * @throws ApiError
   */
  public static getRealtimeConsumerGroupRetryPolicy({
    slug,
    id,
    channel,
    group,
  }: {
    /**
     * Application slug for read consumer-group retry policy.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for read consumer-group retry policy.
     */
    id: string,
    /**
     * Channel name for read consumer-group retry policy.
     */
    channel: string,
    /**
     * Consumer group name for read consumer-group retry policy.
     */
    group: string,
  }): CancelablePromise<RealtimeConsumerGroupRetryStatus> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/retry-policy',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      errors: {
        400: `Invalid delay range or permanent failure codes`,
        404: `Read consumer-group retry policy. Endpoint or group not found`,
        409: `Read consumer-group retry policy. Group conflict, lost claim lease, or retention gap`,
        429: `Read consumer-group retry policy. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Configure consumer-group retries
   * Configure consumer-group retries. Requires app ownership and MFA; writes require admin scope. Policies are disabled by default. Enabled policies apply capped exponential backoff to reported failures with omitted retry_after_seconds. Explicit delays, including zero, override backoff. Exact permanent failure codes dead-letter immediately even with an explicit delay. Jitter samples uniformly from half the capped delay through the capped delay, in integer seconds. Attempt budgets still apply. Changes affect future settlements, not already scheduled retries or expired leases. next_retry_at is the earliest delayed ready snapshot; ordering, pause and capacity can delay its claim further.
   * @returns RealtimeConsumerGroupRetryStatus Configure consumer-group retries. Policy and earliest delayed retry observation
   * @throws ApiError
   */
  public static configureRealtimeConsumerGroupRetryPolicy({
    slug,
    id,
    channel,
    group,
    requestBody,
  }: {
    /**
     * Application slug for configure consumer-group retries.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for configure consumer-group retries.
     */
    id: string,
    /**
     * Channel name for configure consumer-group retries.
     */
    channel: string,
    /**
     * Consumer group name for configure consumer-group retries.
     */
    group: string,
    requestBody: RealtimeConsumerGroupRetryPolicyRequest,
  }): CancelablePromise<RealtimeConsumerGroupRetryStatus> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/retry-policy',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Configure consumer-group retries. Invalid delay range or permanent failure codes`,
        404: `Configure consumer-group retries. Endpoint or group not found`,
        409: `Configure consumer-group retries. Group conflict, lost claim lease, or retention gap`,
        429: `Configure consumer-group retries. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Read dead-letter retention policy
   * Requires app ownership and MFA; writes require admin scope. Zero keeps dead letters indefinitely. Otherwise choose 3600 to 2592000 seconds. Ordered groups require skip policy. Cleanup follows a persisted warning decision by at least 300 seconds; webhook delivery does not gate cleanup. Replay protects the snapshot when it commits before cleanup. Changed policies rearm warnings.
   * @returns RealtimeConsumerGroupRetentionPolicy Retention policy and cumulative expired count
   * @throws ApiError
   */
  public static getRealtimeConsumerGroupDeadLetterRetention({
    slug,
    id,
    channel,
    group,
  }: {
    /**
     * Application slug for read dead-letter retention policy.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for read dead-letter retention policy.
     */
    id: string,
    /**
     * Channel name for read dead-letter retention policy.
     */
    channel: string,
    /**
     * Consumer group name for read dead-letter retention policy.
     */
    group: string,
  }): CancelablePromise<RealtimeConsumerGroupRetentionPolicy> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letter-retention',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      errors: {
        400: `Invalid retention period or incompatible ordered block policy`,
        404: `Read dead-letter retention policy. Endpoint or group not found`,
        409: `Read dead-letter retention policy. Group conflict, lost claim lease, or retention gap`,
        429: `Read dead-letter retention policy. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Configure dead-letter retention
   * Configure dead-letter retention. Requires app ownership and MFA; writes require admin scope. Zero keeps dead letters indefinitely. Otherwise choose 3600 to 2592000 seconds. Ordered groups require skip policy. Cleanup follows a persisted warning decision by at least 300 seconds; webhook delivery does not gate cleanup. Replay protects the snapshot when it commits before cleanup. Changed policies rearm warnings.
   * @returns RealtimeConsumerGroupRetentionPolicy Configure dead-letter retention. Retention policy and cumulative expired count
   * @throws ApiError
   */
  public static configureRealtimeConsumerGroupDeadLetterRetention({
    slug,
    id,
    channel,
    group,
    requestBody,
  }: {
    /**
     * Application slug for configure dead-letter retention.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for configure dead-letter retention.
     */
    id: string,
    /**
     * Channel name for configure dead-letter retention.
     */
    channel: string,
    /**
     * Consumer group name for configure dead-letter retention.
     */
    group: string,
    requestBody: RealtimeConsumerGroupRetentionRequest,
  }): CancelablePromise<RealtimeConsumerGroupRetentionPolicy> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letter-retention',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Configure dead-letter retention. Invalid retention period or incompatible ordered block policy`,
        404: `Configure dead-letter retention. Endpoint or group not found`,
        409: `Configure dead-letter retention. Group conflict, lost claim lease, or retention gap`,
        429: `Configure dead-letter retention. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Read lag alert status
   * Requires app ownership and MFA; writes require admin scope. One policy per group. Zero thresholds disable that metric. Enabled policies require at least one nonzero threshold. Observations and signed webhooks are sampled, including paused/draining groups. Cooldown limits ongoing breach reminders; transitions alert promptly. Identical PUTs preserve observations; changed policies rearm without a recovery backfill. Disabled policies do not evaluate.
   * @returns RealtimeConsumerGroupLagAlert Policy and latest nullable observation
   * @throws ApiError
   */
  public static getRealtimeConsumerGroupLagAlert({
    slug,
    id,
    channel,
    group,
  }: {
    /**
     * Application slug for read lag alert status.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for read lag alert status.
     */
    id: string,
    /**
     * Channel name for read lag alert status.
     */
    channel: string,
    /**
     * Consumer group name for read lag alert status.
     */
    group: string,
  }): CancelablePromise<RealtimeConsumerGroupLagAlert> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/lag-alert',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      errors: {
        400: `Invalid thresholds or cooldown`,
        404: `Read lag alert status. Endpoint or group not found`,
        409: `Read lag alert status. Group conflict, lost claim lease, or retention gap`,
        429: `Read lag alert status. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Configure consumer-group lag thresholds
   * Configure consumer-group lag thresholds. Requires app ownership and MFA; writes require admin scope. One policy per group. Zero thresholds disable that metric. Enabled policies require at least one nonzero threshold. Observations and signed webhooks are sampled, including paused/draining groups. Cooldown limits ongoing breach reminders; transitions alert promptly. Identical PUTs preserve observations; changed policies rearm without a recovery backfill. Disabled policies do not evaluate.
   * @returns RealtimeConsumerGroupLagAlert Configure consumer-group lag thresholds. Policy and latest nullable observation
   * @throws ApiError
   */
  public static configureRealtimeConsumerGroupLagAlert({
    slug,
    id,
    channel,
    group,
    requestBody,
  }: {
    /**
     * Application slug for configure consumer-group lag thresholds.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for configure consumer-group lag thresholds.
     */
    id: string,
    /**
     * Channel name for configure consumer-group lag thresholds.
     */
    channel: string,
    /**
     * Consumer group name for configure consumer-group lag thresholds.
     */
    group: string,
    requestBody: RealtimeConsumerGroupLagAlertConfig,
  }): CancelablePromise<RealtimeConsumerGroupLagAlert> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/lag-alert',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Configure consumer-group lag thresholds. Invalid thresholds or cooldown`,
        404: `Configure consumer-group lag thresholds. Endpoint or group not found`,
        409: `Configure consumer-group lag thresholds. Group conflict, lost claim lease, or retention gap`,
        429: `Configure consumer-group lag thresholds. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Claim work using the claim permission
   * Claim work using the claim permission. Accepts only a group-bound grw_ worker credential. Exact permission is checked in the group transaction; group recreation does not restore access.
   * @returns RealtimeConsumerGroupClaim Claim work using the claim permission. Operation result
   * @throws ApiError
   */
  public static claimRealtimeWorkerGroup({
    groupId,
    requestBody,
  }: {
    /**
     * Consumer group ID for claim work using the claim permission.
     */
    groupId: string,
    requestBody: RealtimeWorkerClaimRequest,
  }): CancelablePromise<RealtimeConsumerGroupClaim> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/realtime/workers/groups/{group_id}/claims',
      path: {
        'group_id': groupId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `Claim work using the claim permission. Credential invalid, expired, rotated or revoked`,
        403: `Claim work using the claim permission. Credential lacks permission`,
        404: `Claim work using the claim permission. Group or credential not found`,
        409: `Claim work using the claim permission. Lease lost or group conflict`,
        429: `Claim work using the claim permission. Admission capacity or event ID capacity reached; realtime_group_deduplication_full includes Retry-After and retry_after_seconds Group rate uses code realtime_group_rate_limited, Retry-After and retry_after_seconds.`,
      },
    });
  }
  /**
   * Settle or renew only this credential’s own lease
   * Requires the exact action permission: complete, retry, renew or release. The credential can settle only leases issued to its stable credential ID. Rotation preserves that ID and claim ownership. New settlements require unexpired claim tokens. Matching receipts return the original result after current authorization, even when the original lease has ended. Expired/revoked credentials cannot settle or replay work.
   * @returns RealtimeConsumerGroupSettlementResult Settle or renew only this credential’s own lease. Operation result
   * @throws ApiError
   */
  public static settleRealtimeWorkerGroupClaim({
    groupId,
    requestBody,
  }: {
    /**
     * Consumer group ID for settle or renew only this credential’s own lease.
     */
    groupId: string,
    requestBody: RealtimeConsumerGroupSettlement,
  }): CancelablePromise<RealtimeConsumerGroupSettlementResult> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/realtime/workers/groups/{group_id}/claims/settle',
      path: {
        'group_id': groupId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `Settle or renew only this credential’s own lease. Credential invalid, expired, rotated or revoked`,
        403: `Settle or renew only this credential’s own lease. Credential lacks permission`,
        404: `Settle or renew only this credential’s own lease. Group or credential not found`,
        409: `Claim lease lost, or idempotency key reused with different contents (realtime_settlement_key_conflict)`,
        429: `Admission, authentication or settlement receipt capacity reached`,
      },
    });
  }
  /**
   * Read failed payloads using the separate dead_letters permission
   * Read failed payloads using the separate dead_letters permission. Accepts only a group-bound grw_ worker credential. Exact permission is checked in the group transaction; group recreation does not restore access.
   * @returns RealtimeConsumerGroupDeadLetter Read failed payloads using the separate dead_letters permission. Operation result
   * @throws ApiError
   */
  public static listRealtimeWorkerGroupDeadLetters({
    groupId,
  }: {
    /**
     * Consumer group ID for read failed payloads using the separate dead_letters permission.
     */
    groupId: string,
  }): CancelablePromise<Array<RealtimeConsumerGroupDeadLetter>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/realtime/workers/groups/{group_id}/dead-letters',
      path: {
        'group_id': groupId,
      },
      errors: {
        401: `Read failed payloads using the separate dead_letters permission. Credential invalid, expired, rotated or revoked`,
        403: `Read failed payloads using the separate dead_letters permission. Credential lacks permission`,
        404: `Read failed payloads using the separate dead_letters permission. Group or credential not found`,
        409: `Read failed payloads using the separate dead_letters permission. Lease lost or group conflict`,
        429: `Read failed payloads using the separate dead_letters permission. Admission or authentication limit reached`,
      },
    });
  }
  /**
   * Replay failed work using the separate replay permission
   * Replay failed work using the separate replay permission. Accepts only a group-bound grw_ worker credential. Exact permission is checked in the group transaction; group recreation does not restore access.
   * @returns void
   * @throws ApiError
   */
  public static replayRealtimeWorkerGroupDeadLetter({
    groupId,
    sequence,
  }: {
    /**
     * Consumer group ID for replay failed work using the separate replay permission.
     */
    groupId: string,
    /**
     * Message sequence for replay failed work using the separate replay permission.
     */
    sequence: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/realtime/workers/groups/{group_id}/dead-letters/{sequence}/replay',
      path: {
        'group_id': groupId,
        'sequence': sequence,
      },
      errors: {
        401: `Replay failed work using the separate replay permission. Credential invalid, expired, rotated or revoked`,
        403: `Replay failed work using the separate replay permission. Credential lacks permission`,
        404: `Replay failed work using the separate replay permission. Group or credential not found`,
        409: `Replay failed work using the separate replay permission. Lease lost or group conflict`,
        429: `Replay failed work using the separate replay permission. Admission or authentication limit reached`,
      },
    });
  }
  /**
   * Recover a gap using the separate resync permission
   * Recover a gap using the separate resync permission. Accepts only a group-bound grw_ worker credential. Exact permission is checked in the group transaction; group recreation does not restore access.
   * @returns void
   * @throws ApiError
   */
  public static resyncRealtimeWorkerGroup({
    groupId,
    requestBody,
  }: {
    /**
     * Consumer group ID for recover a gap using the separate resync permission.
     */
    groupId: string,
    requestBody: RealtimeConsumerGroupResyncRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/realtime/workers/groups/{group_id}/resync',
      path: {
        'group_id': groupId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `Recover a gap using the separate resync permission. Credential invalid, expired, rotated or revoked`,
        403: `Recover a gap using the separate resync permission. Credential lacks permission`,
        404: `Recover a gap using the separate resync permission. Group or credential not found`,
        409: `Recover a gap using the separate resync permission. Lease lost or group conflict`,
        429: `Recover a gap using the separate resync permission. Admission or authentication limit reached`,
      },
    });
  }
  /**
   * List consumer group lag and worker status
   * Backend-only. Requires history preview, account API credentials, app permissions and MFA. Write operations require write scope. Delivery is at least once.
   * @returns RealtimeConsumerGroup Consumer group operation result
   * @throws ApiError
   */
  public static listRealtimeConsumerGroups({
    slug,
    id,
    channel,
  }: {
    /**
     * Application slug for list consumer group lag and worker status.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for list consumer group lag and worker status.
     */
    id: string,
    /**
     * Channel name for list consumer group lag and worker status.
     */
    channel: string,
  }): CancelablePromise<Array<RealtimeConsumerGroup>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      errors: {
        404: `List consumer group lag and worker status. Endpoint or group not found`,
        409: `List consumer group lag and worker status. Group conflict, lost claim lease, or retention gap`,
        429: `List consumer group lag and worker status. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Create a shared backend-worker subscription
   * Create a shared backend-worker subscription. Backend-only. Requires history preview, account API credentials, app permissions and MFA. Write operations require write scope. Delivery is at least once.
   * @returns RealtimeConsumerGroup Create a shared backend-worker subscription. Consumer group operation result
   * @throws ApiError
   */
  public static createRealtimeConsumerGroup({
    slug,
    id,
    channel,
    requestBody,
  }: {
    /**
     * Application slug for create a shared backend-worker subscription.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for create a shared backend-worker subscription.
     */
    id: string,
    /**
     * Channel name for create a shared backend-worker subscription.
     */
    channel: string,
    requestBody: RealtimeConsumerGroupCreateRequest,
  }): CancelablePromise<RealtimeConsumerGroup> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        404: `Create a shared backend-worker subscription. Endpoint or group not found`,
        409: `Create a shared backend-worker subscription. Group conflict, lost claim lease, or retention gap`,
        429: `Create a shared backend-worker subscription. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Read consumer group lag and worker status
   * Read consumer group lag and worker status. Backend-only. Requires history preview, account API credentials, app permissions and MFA. Write operations require write scope. Delivery is at least once.
   * @returns RealtimeConsumerGroup Read consumer group lag and worker status. Consumer group operation result
   * @throws ApiError
   */
  public static getRealtimeConsumerGroup({
    slug,
    id,
    channel,
    group,
  }: {
    /**
     * Application slug for read consumer group lag and worker status.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for read consumer group lag and worker status.
     */
    id: string,
    /**
     * Channel name for read consumer group lag and worker status.
     */
    channel: string,
    /**
     * Consumer group name for read consumer group lag and worker status.
     */
    group: string,
  }): CancelablePromise<RealtimeConsumerGroup> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      errors: {
        404: `Read consumer group lag and worker status. Endpoint or group not found`,
        409: `Read consumer group lag and worker status. Group conflict, lost claim lease, or retention gap`,
        429: `Read consumer group lag and worker status. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Delete a consumer group and its outstanding payload snapshots
   * Delete a consumer group and its outstanding payload snapshots. Backend-only. Requires history preview, account API credentials, app permissions and MFA. Write operations require write scope. Delivery is at least once.
   * @returns void
   * @throws ApiError
   */
  public static deleteRealtimeConsumerGroup({
    slug,
    id,
    channel,
    group,
  }: {
    /**
     * Application slug for delete a consumer group and its outstanding payload snapshots.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for delete a consumer group and its outstanding payload snapshots.
     */
    id: string,
    /**
     * Channel name for delete a consumer group and its outstanding payload snapshots.
     */
    channel: string,
    /**
     * Consumer group name for delete a consumer group and its outstanding payload snapshots.
     */
    group: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      errors: {
        404: `Delete a consumer group and its outstanding payload snapshots. Endpoint or group not found`,
        409: `Delete a consumer group and its outstanding payload snapshots. Group conflict, lost claim lease, or retention gap`,
        429: `Delete a consumer group and its outstanding payload snapshots. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Claim one retained message with an exclusive short lease
   * Returns 204 when paused, caught up, retry-delayed, or all active lease slots are occupied. Returns 429 when the outstanding snapshot cap prevents assignment, and 409 on a retention gap. Claim tokens are returned once and hashed at rest; this operation is excluded from generic response caching. Lease expiry may permit duplicate processing; use idempotent handlers. Does not accept subscription tickets or WebSocket credentials.
   * @returns RealtimeConsumerGroupClaim Claim one retained message with an exclusive short lease. Consumer group operation result
   * @throws ApiError
   */
  public static claimRealtimeConsumerGroup({
    slug,
    id,
    channel,
    group,
    requestBody,
  }: {
    /**
     * Application slug for claim one retained message with an exclusive short lease.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for claim one retained message with an exclusive short lease.
     */
    id: string,
    /**
     * Channel name for claim one retained message with an exclusive short lease.
     */
    channel: string,
    /**
     * Consumer group name for claim one retained message with an exclusive short lease.
     */
    group: string,
    requestBody: RealtimeConsumerGroupClaimRequest,
  }): CancelablePromise<RealtimeConsumerGroupClaim> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        404: `Claim one retained message with an exclusive short lease. Endpoint or group not found`,
        409: `Claim one retained message with an exclusive short lease. Group conflict, lost claim lease, or retention gap`,
        429: `Claim one retained message with an exclusive short lease. Admission capacity or event ID capacity reached; realtime_group_deduplication_full includes Retry-After and retry_after_seconds Group rate uses code realtime_group_rate_limited, Retry-After and retry_after_seconds.`,
      },
    });
  }
  /**
   * Complete, retry or renew an unexpired claim
   * The sequence and opaque token must identify the current unexpired lease. Completion deletes its payload snapshot and increments the group completion count. Retries reaching max_attempts become dead letters. Duplicate settlement or a replaced/expired token returns 409; no exactly-once execution guarantee.
   * @returns RealtimeConsumerGroupSettlementResult Complete, retry or renew an unexpired claim. Consumer group operation result
   * @throws ApiError
   */
  public static settleRealtimeConsumerGroupClaim({
    slug,
    id,
    channel,
    group,
    requestBody,
  }: {
    /**
     * Application slug for complete, retry or renew an unexpired claim.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for complete, retry or renew an unexpired claim.
     */
    id: string,
    /**
     * Channel name for complete, retry or renew an unexpired claim.
     */
    channel: string,
    /**
     * Consumer group name for complete, retry or renew an unexpired claim.
     */
    group: string,
    requestBody: RealtimeConsumerGroupSettlement,
  }): CancelablePromise<RealtimeConsumerGroupSettlementResult> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/claims/settle',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        404: `Complete, retry or renew an unexpired claim. Endpoint or group not found`,
        409: `Complete, retry or renew an unexpired claim. Claim lease lost, or idempotency key reused with different contents (realtime_settlement_key_conflict)`,
        429: `Complete, retry or renew an unexpired claim. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * List bounded failed message snapshots
   * List bounded failed message snapshots. Backend-only. Requires history preview, account API credentials, app permissions and MFA. Write operations require write scope. Delivery is at least once.
   * @returns RealtimeConsumerGroupDeadLetter List bounded failed message snapshots. Consumer group operation result
   * @throws ApiError
   */
  public static listRealtimeConsumerGroupDeadLetters({
    slug,
    id,
    channel,
    group,
  }: {
    /**
     * Application slug for list bounded failed message snapshots.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for list bounded failed message snapshots.
     */
    id: string,
    /**
     * Channel name for list bounded failed message snapshots.
     */
    channel: string,
    /**
     * Consumer group name for list bounded failed message snapshots.
     */
    group: string,
  }): CancelablePromise<Array<RealtimeConsumerGroupDeadLetter>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      errors: {
        404: `List bounded failed message snapshots. Endpoint or group not found`,
        409: `List bounded failed message snapshots. Group conflict, lost claim lease, or retention gap`,
        429: `List bounded failed message snapshots. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Download a read-only dead-letter JSONL page
   * Account ownership, read scope, completed MFA and realtime history preview required. Returns one manifest line followed by zero to 128 dead_letter lines, ordered by numeric sequence. Payload is the retained snapshot with base64 data and metadata. No claim or credential tokens. Every page uses a repeatable-read read-only transaction; export never claims, replays, cleans up or extends retention. First page captures group_id, through_sequence and snapshot_at. Resume with those three values and next_after_sequence; keep failure_code filters unchanged. Later dead-letter transitions are excluded by the snapshot cutoff. Replay, deletion and retention cleanup can remove records between pages; this is a live export, not an immutable server archive. A recreated group or rewound assignment boundary returns 409 realtime_dead_letter_export_changed. Verify the manifest count and complete transfer before advancing the cursor. Link headers provide the next page. Empty exports still contain a manifest. At most 2 MiB per page. Persist downloads externally for archival.
   * @returns binary JSONL attachment. First line matches RealtimeDeadLetterExportManifest; subsequent lines match RealtimeDeadLetterExportRecord.
   * @throws ApiError
   */
  public static exportRealtimeConsumerGroupDeadLetters({
    slug,
    id,
    channel,
    group,
    afterSequence = '0',
    groupId,
    throughSequence,
    snapshotAt,
    failureCode,
    limit = 32,
  }: {
    /**
     * Application slug for download a read-only dead-letter JSONL page.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for download a read-only dead-letter JSONL page.
     */
    id: string,
    /**
     * Channel name for download a read-only dead-letter JSONL page.
     */
    channel: string,
    /**
     * Consumer group name for download a read-only dead-letter JSONL page.
     */
    group: string,
    /**
     * Exclusive numeric cursor. Resume using next_after_sequence from the last complete page.
     */
    afterSequence?: string,
    /**
     * Immutable group identity. Supply together with through_sequence and snapshot_at when resuming.
     */
    groupId?: string,
    /**
     * Inclusive captured assignment boundary. Supply together with group_id and snapshot_at.
     */
    throughSequence?: string,
    /**
     * Original manifest snapshot cutoff. Supply together with group_id and through_sequence.
     */
    snapshotAt?: string,
    /**
     * Repeat for exact safe failure-code matches; omitted includes all retained failures.
     */
    failureCode?: Array<string>,
    /**
     * Maximum records after the manifest.
     */
    limit?: number,
  }): CancelablePromise<Blob> {
    return __request(OpenAPI, {
      responseType: 'blob',
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/export',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      query: {
        'after_sequence': afterSequence,
        'group_id': groupId,
        'through_sequence': throughSequence,
        'snapshot_at': snapshotAt,
        'failure_code': failureCode,
        'limit': limit,
      },
      errors: {
        400: `Invalid cursor or filters`,
        401: `Authentication required`,
        403: `Read access or MFA required`,
        404: `Preview or owned endpoint or group unavailable`,
        409: `Group identity or export boundary changed (realtime_dead_letter_export_changed)`,
        429: `Admission or export page capacity reached`,
      },
    });
  }
  /**
   * Preview filtered dead letters
   * Read scope, app ownership and MFA required. Read-only selection of materialized dead snapshots. Filters use exact failure codes, exclusive after_sequence and inclusive through_sequence. Default and maximum limit 32. has_more allows keyset pagination using the last returned sequence. Expired exhausted leases are excluded until materialized as dead. No replay or expiry grace is reserved; candidates can change after preview.
   * @returns RealtimeDeadLetterPreview Bounded recovery result
   * @throws ApiError
   */
  public static previewRealtimeConsumerGroupDeadLetters({
    slug,
    id,
    channel,
    group,
    requestBody,
  }: {
    /**
     * Application slug for preview filtered dead letters.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for preview filtered dead letters.
     */
    id: string,
    /**
     * Channel name for preview filtered dead letters.
     */
    channel: string,
    /**
     * Consumer group name for preview filtered dead letters.
     */
    group: string,
    requestBody: RealtimeDeadLetterPreviewRequest,
  }): CancelablePromise<RealtimeDeadLetterPreview> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/preview',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid filters or selection`,
        404: `Preview filtered dead letters. Endpoint or group not found`,
        409: `Preview filtered dead letters. Group conflict, lost claim lease, or retention gap`,
        429: `Preview filtered dead letters. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * List replay jobs
   * Account ownership and MFA required. Read scope for listing; deploy-write scope for creation. Creation freezes up to 128 current failure identities matching the filters. Rate is an upper limit with no catch-up burst. Four running or paused jobs per group; recent terminal jobs retained in bounded history. Disabled endpoints and inactive owners suspend processing. Skips are terminal per item. Database failures roll back progress and retry on subsequent passes. Creation is not idempotent: inspect the job list after an uncertain response before retrying.
   * @returns RealtimeReplayJob Replay job result
   * @throws ApiError
   */
  public static listRealtimeReplayJobs({
    slug,
    id,
    channel,
    group,
  }: {
    /**
     * Application slug for list replay jobs.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for list replay jobs.
     */
    id: string,
    /**
     * Channel name for list replay jobs.
     */
    channel: string,
    /**
     * Consumer group name for list replay jobs.
     */
    group: string,
  }): CancelablePromise<Array<RealtimeReplayJob>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      errors: {
        400: `Invalid filters, empty selection or rate`,
        404: `Group not found`,
        409: `Active job quota reached`,
      },
    });
  }
  /**
   * Create a background dead-letter replay job
   * Create a background dead-letter replay job. Account ownership and MFA required. Read scope for listing; deploy-write scope for creation. Creation freezes up to 128 current failure identities matching the filters. Rate is an upper limit with no catch-up burst. Four running or paused jobs per group; recent terminal jobs retained in bounded history. Disabled endpoints and inactive owners suspend processing. Skips are terminal per item. Database failures roll back progress and retry on subsequent passes. Creation is not idempotent: inspect the job list after an uncertain response before retrying.
   * @returns RealtimeReplayJob Create a background dead-letter replay job. Replay job result
   * @throws ApiError
   */
  public static createRealtimeReplayJob({
    slug,
    id,
    channel,
    group,
    requestBody,
  }: {
    /**
     * Application slug for create a background dead-letter replay job.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for create a background dead-letter replay job.
     */
    id: string,
    /**
     * Channel name for create a background dead-letter replay job.
     */
    channel: string,
    /**
     * Consumer group name for create a background dead-letter replay job.
     */
    group: string,
    requestBody: RealtimeReplayJobRequest,
  }): CancelablePromise<RealtimeReplayJob> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Create a background dead-letter replay job. Invalid filters, empty selection or rate`,
        404: `Create a background dead-letter replay job. Group not found`,
        409: `Create a background dead-letter replay job. Active job quota reached`,
      },
    });
  }
  /**
   * Create a background replay eligibility report
   * Account ownership, MFA and deploy-write scope required. Captures current failure identities and checks each at the configured rate without expiring leases, replaying messages or changing the queue. Uses checking/check_paused states. Dry runs share the four-active-job quota and bounded history with real jobs. Results are observations at individual check times, not reservations. No replay lifecycle webhooks are emitted for dry runs. Start a real job from a completed report using the start control action; only reviewed eligible identities are copied and rechecked during actual replay. Empty selections are invalid. Creation is not idempotent; inspect recent jobs after an uncertain response before retrying.
   * @returns RealtimeReplayJob Create a background replay eligibility report. Replay job result
   * @throws ApiError
   */
  public static createRealtimeReplayDryRun({
    slug,
    id,
    channel,
    group,
    requestBody,
  }: {
    /**
     * Application slug for create a background replay eligibility report.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for create a background replay eligibility report.
     */
    id: string,
    /**
     * Channel name for create a background replay eligibility report.
     */
    channel: string,
    /**
     * Consumer group name for create a background replay eligibility report.
     */
    group: string,
    requestBody: RealtimeReplayJobRequest,
  }): CancelablePromise<RealtimeReplayJob> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs/dry-run',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Create a background replay eligibility report. Invalid filters, empty selection or rate`,
        404: `Create a background replay eligibility report. Group not found`,
        409: `Create a background replay eligibility report. Active job quota reached`,
      },
    });
  }
  /**
   * Pause, resume, cancel or start a reviewed replay
   * Account deploy-write scope, ownership and MFA required. Pause/resume/cancel apply to active real or dry-run jobs. Start requires a completed dry run with eligible failures, and creates a real replay at the report rate from only those exact reviewed identities. Rechecks identity, deadlines and ordering before each real replay. Repeated start returns the same job while retained; if that job was pruned it returns 409 and never starts another. If the report itself was pruned it returns 404. Promotion and the report link commit together. Both modes share the four-active-job quota. Terminal jobs reject pause/resume/cancel. Cancellation preserves committed replays.
   * @returns RealtimeReplayJob Pause, resume, cancel or start a reviewed replay. Replay job result
   * @throws ApiError
   */
  public static controlRealtimeReplayJob({
    slug,
    id,
    channel,
    group,
    job,
    action,
  }: {
    /**
     * Application slug for pause, resume, cancel or start a reviewed replay.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for pause, resume, cancel or start a reviewed replay.
     */
    id: string,
    /**
     * Channel name for pause, resume, cancel or start a reviewed replay.
     */
    channel: string,
    /**
     * Consumer group name for pause, resume, cancel or start a reviewed replay.
     */
    group: string,
    /**
     * Job for pause, resume, cancel or start a reviewed replay.
     */
    job: string,
    /**
     * Action for pause, resume, cancel or start a reviewed replay.
     */
    action: 'pause' | 'resume' | 'cancel' | 'start',
  }): CancelablePromise<RealtimeReplayJob> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/replay-jobs/{job}/{action}',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
        'job': job,
        'action': action,
      },
      errors: {
        400: `Invalid job or action`,
        404: `Job not found`,
        409: `Job is terminal, report not completed or not a dry run, no eligible failures, active quota reached, or previously started job pruned`,
      },
    });
  }
  /**
   * Replay selected dead letters
   * Deploy-write scope, app ownership and MFA required. Select 1 to 32 unique sequences with exact dead_since timestamps from preview and the immutable group UUID. Preserves ordering checks and resets attempts for successful replays. Outcomes are replayed, ordering_blocked, stale, not_dead, missing, deadline_expired or invalid_deadline. Missing may mean retention cleanup; no cause is inferred. Recognized per-item conflicts do not prevent other selected replays. Unexpected failures roll back the whole transaction. Group UUID mismatch returns 409. A replayed item gets a fresh fencing token on its next claim. Do not automatically retry an uncertain response.
   * @returns RealtimeDeadLetterBulkReplayResult Replay selected dead letters. Bounded recovery result
   * @throws ApiError
   */
  public static bulkReplayRealtimeConsumerGroupDeadLetters({
    slug,
    id,
    channel,
    group,
    requestBody,
  }: {
    /**
     * Application slug for replay selected dead letters.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for replay selected dead letters.
     */
    id: string,
    /**
     * Channel name for replay selected dead letters.
     */
    channel: string,
    /**
     * Consumer group name for replay selected dead letters.
     */
    group: string,
    requestBody: RealtimeDeadLetterBulkReplayRequest,
  }): CancelablePromise<RealtimeDeadLetterBulkReplayResult> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/replay',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Replay selected dead letters. Invalid filters or selection`,
        404: `Replay selected dead letters. Endpoint or group not found`,
        409: `Replay selected dead letters. Group conflict, lost claim lease, or retention gap`,
        429: `Replay selected dead letters. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Replay one dead letter with a fresh retry budget
   * Replay one dead letter with a fresh retry budget. Backend-only. Requires history preview, account API credentials, app permissions and MFA. Write operations require write scope. Delivery is at least once.
   * @returns void
   * @throws ApiError
   */
  public static replayRealtimeConsumerGroupDeadLetter({
    slug,
    id,
    channel,
    group,
    sequence,
  }: {
    /**
     * Application slug for replay one dead letter with a fresh retry budget.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for replay one dead letter with a fresh retry budget.
     */
    id: string,
    /**
     * Channel name for replay one dead letter with a fresh retry budget.
     */
    channel: string,
    /**
     * Consumer group name for replay one dead letter with a fresh retry budget.
     */
    group: string,
    /**
     * Message sequence for replay one dead letter with a fresh retry budget.
     */
    sequence: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/{sequence}/replay',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
        'sequence': sequence,
      },
      errors: {
        404: `Replay one dead letter with a fresh retry budget. Endpoint or group not found`,
        409: `Replay one dead letter with a fresh retry budget. Group conflict, lost claim lease, or retention gap`,
        429: `Replay one dead letter with a fresh retry budget. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Explicitly recover an unassigned retention gap
   * Requires expected_sequence to equal the assignment cursor, no ready/leased messages, and after_sequence to equal oldest_sequence minus one and advance the cursor. Recover missing work from application state first. Preserves dead letters and completed count; does not claim skipped work was processed.
   * @returns void
   * @throws ApiError
   */
  public static resyncRealtimeConsumerGroup({
    slug,
    id,
    channel,
    group,
    requestBody,
  }: {
    /**
     * Application slug for explicitly recover an unassigned retention gap.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for explicitly recover an unassigned retention gap.
     */
    id: string,
    /**
     * Channel name for explicitly recover an unassigned retention gap.
     */
    channel: string,
    /**
     * Consumer group name for explicitly recover an unassigned retention gap.
     */
    group: string,
    requestBody: RealtimeConsumerGroupResyncRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/resync',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'group': group,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        404: `Explicitly recover an unassigned retention gap. Endpoint or group not found`,
        409: `Explicitly recover an unassigned retention gap. Group conflict, lost claim lease, or retention gap`,
        429: `Explicitly recover an unassigned retention gap. Endpoint or group admission capacity reached`,
      },
    });
  }
  /**
   * Read the latest actual ACK for a named durable consumer
   * Read-only and does not create or refresh a checkpoint. Requires history preview.
   * ACKs are consumer assertions; baselines and resets are not processing receipts.
   * Only the most recent advancing ACK is retained, not an individual-message ledger.
   *
   * @returns RealtimeAcknowledgement Current checkpoint and latest actual advancing ACK
   * @throws ApiError
   */
  public static getRealtimeChannelAcknowledgement({
    slug,
    id,
    channel,
    principal,
    subscription,
  }: {
    /**
     * Application slug for read the latest actual ACK for a named durable consumer.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for read the latest actual ACK for a named durable consumer.
     */
    id: string,
    /**
     * Channel name for read the latest actual ACK for a named durable consumer.
     */
    channel: string,
    /**
     * Principal for read the latest actual ACK for a named durable consumer.
     */
    principal: string,
    /**
     * Subscription for read the latest actual ACK for a named durable consumer.
     */
    subscription: string,
  }): CancelablePromise<RealtimeAcknowledgement> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/acknowledgement',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      query: {
        'principal': principal,
        'subscription': subscription,
      },
      errors: {
        404: `Consumer checkpoint missing or expired`,
      },
    });
  }
  /**
   * Read the account's retained realtime history snapshot
   * Preview only; requires usage read scope and MFA for interactive sessions.
   * Counts current retained message rows and payload bytes for this account,
   * including expired rows awaiting cleanup. Replayable counts apply the
   * channel's contiguous retention floor. These values are informational
   * snapshots, not billed usage or physical database allocation.
   *
   * @returns ManagedRealtimeHistoryUsageResponse Account-scoped retained history snapshot; Cache-Control no-store
   * @returns Problem Authentication or history usage error
   * @throws ApiError
   */
  public static getManagedRealtimeHistoryUsage(): CancelablePromise<ManagedRealtimeHistoryUsageResponse | Problem> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/realtime-history-usage',
    });
  }
  /**
   * List managed realtime endpoints for this app.
   * @returns ManagedRealtimeEndpointResponse The configured managed realtime endpoints.
   * @throws ApiError
   */
  public static listManagedRealtimeEndpoints({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<Array<ManagedRealtimeEndpointResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Create a managed realtime endpoint.
   * Registers the callback contract used by realtimed for connect,
   * message, and disconnect events. Callback and optional client
   * credentials are sealed at rest and never returned in plaintext.
   *
   * @returns ManagedRealtimeEndpointResponse Managed realtime endpoint created.
   * @throws ApiError
   */
  public static createManagedRealtimeEndpoint({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateManagedRealtimeEndpointRequest,
  }): CancelablePromise<ManagedRealtimeEndpointResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        403: `code: plan_realtime_quota — per-app or per-account managed realtime endpoint limit reached.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Fetch one managed realtime endpoint.
   * @returns ManagedRealtimeEndpointResponse The managed realtime endpoint.
   * @throws ApiError
   */
  public static getManagedRealtimeEndpoint({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<ManagedRealtimeEndpointResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Partially update a managed realtime endpoint.
   * @returns ManagedRealtimeEndpointResponse The updated managed realtime endpoint.
   * @throws ApiError
   */
  public static updateManagedRealtimeEndpoint({
    slug,
    id,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: UpdateManagedRealtimeEndpointRequest,
  }): CancelablePromise<ManagedRealtimeEndpointResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Delete a managed realtime endpoint.
   * @returns void
   * @throws ApiError
   */
  public static deleteManagedRealtimeEndpoint({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List live connections for a managed realtime endpoint.
   * Returns a bounded point-in-time inventory, optionally filtered by
   * channel or authenticated principal. Results are ordered by connection
   * ID; use the opaque `next_cursor` value to continue a truncated page.
   * `partial` is true when one or more active realtime nodes could not be
   * queried; healthy node results remain in the response.
   *
   * @returns ManagedRealtimeConnectionListResponse The live connection inventory.
   * @throws ApiError
   */
  public static listManagedRealtimeConnections({
    slug,
    id,
    limit = 100,
    channel,
    principal,
    cursor,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Maximum number of live connections to return.
     */
    limit?: number,
    /**
     * Return only connections subscribed to this channel.
     */
    channel?: string,
    /**
     * Return only connections authenticated as this principal.
     */
    principal?: string,
    /**
     * Opaque next_cursor returned by a previous inventory response.
     */
    cursor?: string,
  }): CancelablePromise<ManagedRealtimeConnectionListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/connections',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'limit': limit,
        'channel': channel,
        'principal': principal,
        'cursor': cursor,
      },
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Count a managed realtime channel's subscribers and presence members.
   * Returns fleet-wide counts for each requested channel (ADR-1324).
   * `subscribers` counts every open subscription: WebSocket, read-only
   * ticket and SSE. `presence_members` counts distinct presence members;
   * a principal-scoped member with several connections counts once.
   * Counts are a point-in-time snapshot. `partial` is true when one or
   * more active realtime nodes could not be asked; the counts then cover
   * the nodes that answered.
   *
   * @returns ManagedRealtimeOccupancyResponse Channel occupancy.
   * @throws ApiError
   */
  public static getManagedRealtimeOccupancy({
    slug,
    id,
    channel,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Channel to count; repeat the parameter for up to 20 channels.
     */
    channel: Array<string>,
  }): CancelablePromise<ManagedRealtimeOccupancyResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/occupancy',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'channel': channel,
      },
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Close a bounded or explicitly all-matching set of live managed realtime connections.
   * Selects connections from a point-in-time fleet inventory by channel,
   * principal, or explicit connection IDs. `dry_run` returns the selected
   * connections without closing them. A non-dry-run request fails with
   * `409 conflict` when the inventory is partial unless `allow_partial`
   * is true; this prevents an unavailable node from making a drain look
   * complete. Set `all` to select every matching connection, up to the
   * server safety cap of 10,000; `all` cannot be combined with
   * `connection_ids`; any supplied `limit` is ignored in all mode, and
   * the request returns `409 conflict` when the cap would be exceeded.
   *
   * @returns ManagedRealtimeDrainResponse The durable drain operation was accepted for background execution.
   * @throws ApiError
   */
  public static drainManagedRealtimeConnections({
    slug,
    id,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: ManagedRealtimeDrainRequest,
    /**
     * Replays the accepted operation for 24 hours when the request is retried.
     */
    idempotencyKey?: string,
  }): CancelablePromise<ManagedRealtimeDrainResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/connections/drain',
      path: {
        'slug': slug,
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Get the durable result of a realtime connection drain.
   * Returns a previously recorded drain result scoped to the application and endpoint.
   * @returns ManagedRealtimeDrainResponse The persisted drain result.
   * @throws ApiError
   */
  public static getManagedRealtimeDrainOperation({
    slug,
    id,
    drainId,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Durable identifier returned by the drain request.
     */
    drainId: string,
  }): CancelablePromise<ManagedRealtimeDrainResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/connections/drain/{drain_id}',
      path: {
        'slug': slug,
        'id': id,
        'drain_id': drainId,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Rotate a static bearer credential with a bounded overlap window.
   * Promotes the supplied credential to current and accepts the previous
   * credential until previous_token_expires_at. Credentials are sealed at
   * rest and never returned in plaintext.
   *
   * @returns RotateManagedRealtimeAuthResponse Static bearer credential rotation started.
   * @throws ApiError
   */
  public static rotateManagedRealtimeAuth({
    slug,
    id,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: RotateManagedRealtimeAuthRequest,
  }): CancelablePromise<RotateManagedRealtimeAuthResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/auth/rotate',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Issue a short-lived channel subscription ticket
   * Operator preview. Backend-only app-scoped write with MFA. The backend authorizes the principal and explicit channels before issuing. Tickets allow retained subscriptions only and are returned once with no-store.
   * @returns RealtimeTicketResponse Issued bearer ticket; do not log or cache.
   * @throws ApiError
   */
  public static issueRealtimeSubscriptionTicket({
    slug,
    id,
    requestBody,
  }: {
    /**
     * Application slug for issue a short-lived channel subscription ticket.
     */
    slug: string,
    /**
     * Realtime endpoint or webhook ID for issue a short-lived channel subscription ticket.
     */
    id: string,
    requestBody: RealtimeTicketRequest,
  }): CancelablePromise<RealtimeTicketResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/tickets',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
    });
  }
  /**
   * Revoke every live subscription ticket of one principal
   * Operator preview. Backend-only app-scoped write with MFA, for example at logout. Connections using a revoked ticket close within about a second, when they next re-read their ticket. Expired tickets are not counted.
   * @returns RealtimeTicketsRevokeResponse Number of live tickets revoked.
   * @throws ApiError
   */
  public static revokeRealtimeSubscriptionTickets({
    slug,
    id,
    principal,
  }: {
    /**
     * Application slug owning the endpoint whose principal tickets are revoked.
     */
    slug: string,
    /**
     * Realtime endpoint whose principal tickets are revoked.
     */
    id: string,
    /**
     * Principal whose tickets are revoked.
     */
    principal: string,
  }): CancelablePromise<RealtimeTicketsRevokeResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/tickets',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'principal': principal,
      },
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Revoke one subscription ticket
   * Operator preview. Backend-only app-scoped write with MFA. Uses the ticket_id returned when the ticket was issued, so the backend never needs to keep the bearer token. Connections using the ticket close within about a second.
   * @returns void
   * @throws ApiError
   */
  public static revokeRealtimeSubscriptionTicket({
    slug,
    id,
    ticket,
  }: {
    /**
     * Application slug owning the endpoint of the revoked ticket.
     */
    slug: string,
    /**
     * Realtime endpoint that issued the revoked ticket.
     */
    id: string,
    /**
     * ticket_id returned when the ticket was issued.
     */
    ticket: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/tickets/{ticket}',
      path: {
        'slug': slug,
        'id': id,
        'ticket': ticket,
      },
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
      },
    });
  }
  /**
   * Read durable channel denies and session cutoff for one principal.
   * Requires MFA and read scope. Operator preview requires FAAS_REALTIME_ACCESS_PREVIEW_ENABLED=1 on apid and every realtime node. OIDC endpoints only. Revision zero means no stored controls. Responses contain no token or connection identifiers.
   * @returns ManagedRealtimeAccessResponse Durable controls; not proof that every live node has applied them.
   * @throws ApiError
   */
  public static getManagedRealtimeAccess({
    slug,
    id,
    principal,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Verified OIDC principal whose durable access controls are managed.
     */
    principal: string,
  }): CancelablePromise<ManagedRealtimeAccessResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/principals/{principal}/access',
      path: {
        'slug': slug,
        'id': id,
        'principal': principal,
      },
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Revoke channel access or older sessions, or remove a channel deny.
   * Requires MFA and write scope. OIDC operator preview must be enabled on apid and every realtime node. Channel revocation also denies its private activity scopes. Revoke_sessions records a monotonic server-time cutoff; tokens require an integer iat strictly after it. Nodes refresh access state every second and fail closed on refresh failure. Restoration does not grant app membership or automatically resubscribe. Success confirms persistence, not instantaneous fleet convergence.
   * @returns ManagedRealtimeAccessResponse Committed access controls.
   * @throws ApiError
   */
  public static changeManagedRealtimeAccess({
    slug,
    id,
    principal,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Verified OIDC principal whose durable access controls are managed.
     */
    principal: string,
    requestBody: ManagedRealtimeAccessRequest,
  }): CancelablePromise<ManagedRealtimeAccessResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/principals/{principal}/access',
      path: {
        'slug': slug,
        'id': id,
        'principal': principal,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Revoke the predecessor static bearer credential immediately.
   * @returns FinalizeManagedRealtimeAuthResponse Static bearer credential rotation finalized.
   * @throws ApiError
   */
  public static finalizeManagedRealtimeAuth({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<FinalizeManagedRealtimeAuthResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/auth/rotate/finalize',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Send a message to one live managed realtime connection.
   * @returns any Message accepted by the connection owner.
   * @throws ApiError
   */
  public static sendManagedRealtimeConnection({
    slug,
    id,
    connectionId,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Live connection identifier returned by the callback event.
     */
    connectionId: string,
    requestBody: ManagedRealtimeMessageRequest,
  }): CancelablePromise<any> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/connections/{connection_id}/send',
      path: {
        'slug': slug,
        'id': id,
        'connection_id': connectionId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Close one live managed realtime connection.
   * @returns void
   * @throws ApiError
   */
  public static closeManagedRealtimeConnection({
    slug,
    id,
    connectionId,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Live socket identifier to close on its owning realtime node.
     */
    connectionId: string,
    requestBody?: ManagedRealtimeCloseRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/connections/{connection_id}/close',
      path: {
        'slug': slug,
        'id': id,
        'connection_id': connectionId,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Subscribe one live connection to a channel.
   * @returns void
   * @throws ApiError
   */
  public static subscribeManagedRealtimeConnection({
    slug,
    id,
    connectionId,
    channel,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Live connection identifier returned in realtime callback events.
     */
    connectionId: string,
    /**
     * Channel to add or remove for the live connection.
     */
    channel: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/connections/{connection_id}/subscriptions/{channel}',
      path: {
        'slug': slug,
        'id': id,
        'connection_id': connectionId,
        'channel': channel,
      },
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Remove one live connection from a channel.
   * @returns void
   * @throws ApiError
   */
  public static unsubscribeManagedRealtimeConnection({
    slug,
    id,
    connectionId,
    channel,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Live connection identifier returned in realtime callback events.
     */
    connectionId: string,
    /**
     * Channel to add or remove for the live connection.
     */
    channel: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/connections/{connection_id}/subscriptions/{channel}',
      path: {
        'slug': slug,
        'id': id,
        'connection_id': connectionId,
        'channel': channel,
      },
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Publish a message to live or resumable channel subscribers.
   * delivery=live (the default) fans out to live raw-frame subscribers. delivery=retained is preview-only and requires FAAS_REALTIME_RETAINED_PREVIEW_ENABLED=1 on apid; it accepts at most 4096 decoded bytes and requires an Idempotency-Key. Retained delivery commits the message to the ordered channel log before fan-out and returns its sequence. V2 subscribers read the committed log in sequence; a best-effort wake reduces latency while bounded polling recovers missed wakes. The retained log remains authoritative if live fan-out is incomplete. The resume protocol separately requires FAAS_REALTIME_RESUME_PREVIEW_ENABLED=1 on realtimed.
   * A supplied Idempotency-Key binds the publish to delivery mode, decoded payload and binary flag for 24 hours. Replays return the original response and do not retry recipients that missed a partial publish. Reusing a key with a different mode or payload returns 409. An in-flight or uncertain reservation also returns 409 and is not run again while the key is active. Queue admission does not confirm client receipt.
   * @returns ManagedRealtimePublishResponse Per-recipient queue outcomes; retained publishes also include the committed channel sequence.
   * @throws ApiError
   */
  public static publishManagedRealtimeChannel({
    slug,
    id,
    channel,
    requestBody,
    delivery = 'live',
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Channel that receives the published message.
     */
    channel: string,
    requestBody: ManagedRealtimeMessageRequest,
    /**
     * Use retained to commit a sequenced message before live fan-out; this is preview-only and requires an Idempotency-Key.
     */
    delivery?: 'live' | 'retained',
    /**
     * Stable printable-ASCII key for retrying this publish; reuse it only with the same delivery mode, payload, and binary flag.
     */
    idempotencyKey?: string,
  }): CancelablePromise<ManagedRealtimePublishResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/publish',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      query: {
        'delivery': delivery,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Append one ordered message to retained channel history.
   * Disabled by default; an operator must set FAAS_REALTIME_RETAINED_PREVIEW_ENABLED=1 on apid. Retained writes reach only opt-in v2 WebSocket subscribers when the separate realtimed resume preview is enabled. The sequence is committed before the response; idempotency applies while the message remains retained. At most 32 channels and 1024 messages per channel are retained per endpoint, for up to 24 hours.
   * @returns ManagedRealtimeRetainedMessageResponse Message committed to the channel log.
   * @throws ApiError
   */
  public static appendManagedRealtimeRetainedMessage({
    slug,
    id,
    channel,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Endpoint-scoped channel.
     */
    channel: string,
    requestBody: ManagedRealtimeRetainedMessageRequest,
  }): CancelablePromise<ManagedRealtimeRetainedMessageResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/retained-messages',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Read a page of retained channel history after a sequence.
   * Disabled by default; an operator must set FAAS_REALTIME_RETAINED_PREVIEW_ENABLED=1 on apid. A cursor older than retained history returns 410 with code history_unavailable. This management API is not a WebSocket resume protocol.
   * @returns ManagedRealtimeRetainedHistoryResponse A consistent page of retained messages.
   * @throws ApiError
   */
  public static readManagedRealtimeRetainedMessages({
    slug,
    id,
    channel,
    after,
    limit = 100,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Endpoint-scoped channel.
     */
    channel: string,
    /**
     * Last processed sequence; zero starts at the first retained message.
     */
    after: number,
    /**
     * Maximum number of retained messages to return.
     */
    limit?: number,
  }): CancelablePromise<ManagedRealtimeRetainedHistoryResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/retained-messages',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      query: {
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `code: realtime_invalid — malformed managed realtime endpoint URL, path, or credential.`,
        401: `code: unauthorized`,
        402: `code: plan_realtime_not_allowed — the plan does not include managed realtime endpoints.`,
        404: `code: not_found`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Send to a verified principal, optionally retaining a notification with push fallback.
   * Retained sends require the retained-history preview gate and a stable message ID. Notification category is part of deduplication and defaults to notifications. Push preferences are evaluated before provider delivery; ACKs cancel queued fallback. Live sends can request receipts instead of retention.
   * @returns ManagedRealtimePrincipalSendResponse Accepted; durable reports storage acceptance and queued reports live connection queueing.
   * @throws ApiError
   */
  public static sendManagedRealtimePrincipal({
    slug,
    id,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody: ManagedRealtimePrincipalMessageRequest,
  }): CancelablePromise<ManagedRealtimePrincipalSendResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/principals:send',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Read a user notification preference document.
   * Requires the retained-history preview gate. Defaults enable all categories and devices. Preferences apply to built-in push; inbox retention and ACKs remain independent. Quiet-hour deferrals do not consume attempts. At most 256 preference documents per endpoint.
   * @returns RealtimeNotificationPreferences Current preference document.
   * @throws ApiError
   */
  public static getManagedRealtimeNotificationPreferences({
    slug,
    id,
    principal,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Verified push principal ID
     */
    principal: string,
  }): CancelablePromise<RealtimeNotificationPreferences> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/preferences',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'principal': principal,
      },
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
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Replace notification preferences across user devices.
   * Replaces the principal preference document under the retained-history preview gate. Built-in push uses these preferences independently of inbox retention and ACKs. Quiet-hour deferrals consume no attempts. Each endpoint retains at most 256 documents.
   * @returns RealtimeNotificationPreferences Saved notification preference document.
   * @throws ApiError
   */
  public static putManagedRealtimeNotificationPreferences({
    slug,
    id,
    principal,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Verified push principal ID
     */
    principal: string,
    requestBody: RealtimeNotificationPreferences,
  }): CancelablePromise<RealtimeNotificationPreferences> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/preferences',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'principal': principal,
      },
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
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * List realtime push providers.
   * Requires the retained-history preview gate. Credentials and tokens are never returned. Delivery history includes the latest 100 records for the principal; sent means provider acceptance, not user delivery.
   * @returns ManagedRealtimePushProvider Push metadata.
   * @throws ApiError
   */
  public static listManagedRealtimePushProviders({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<Array<ManagedRealtimePushProvider>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/providers',
      path: {
        'slug': slug,
        'id': id,
      },
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
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * List realtime push devices.
   * Lists registered devices under the retained-history preview gate. Device tokens and provider credentials are omitted.
   * @returns ManagedRealtimePushDevice Registered push device metadata.
   * @throws ApiError
   */
  public static listManagedRealtimePushDevices({
    slug,
    id,
    principal,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Verified push principal ID
     */
    principal: string,
  }): CancelablePromise<Array<ManagedRealtimePushDevice>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/devices',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'principal': principal,
      },
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
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * List realtime push deliveries.
   * Lists the latest 100 push delivery records for the principal under the retained-history preview gate. Provider acceptance does not confirm delivery to the user. Tokens and credentials are omitted.
   * @returns ManagedRealtimePushDelivery Recent push delivery attempt metadata.
   * @throws ApiError
   */
  public static listManagedRealtimePushDeliveries({
    slug,
    id,
    principal,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Verified push principal ID
     */
    principal: string,
  }): CancelablePromise<Array<ManagedRealtimePushDelivery>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/deliveries',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'principal': principal,
      },
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
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Configure or disable a push provider.
   * Provider credentials are sealed at rest. Disabling cancels queued deliveries; re-enabling does not replay them. The retained-history preview gate is required.
   * @returns any Provider configured; credentials are omitted.
   * @throws ApiError
   */
  public static configureManagedRealtimePushProvider({
    slug,
    id,
    provider,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Push provider name
     */
    provider: 'fcm' | 'apns' | 'webpush',
    requestBody: ManagedRealtimePushProviderRequest,
  }): CancelablePromise<{
    provider: 'fcm' | 'apns' | 'webpush';
    enabled: boolean;
  }> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/providers/{provider}',
      path: {
        'slug': slug,
        'id': id,
        'provider': provider,
      },
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
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Register or rotate a device push token.
   * Requires the retained-history preview gate. Registration is limited to 16 devices per principal and 1024 per endpoint. A provider must be enabled before registration. Identical registrations preserve pending deliveries; changed tokens cancel old work.
   * @returns any Registration updated; token is omitted.
   * @throws ApiError
   */
  public static registerManagedRealtimePushDevice({
    slug,
    id,
    principal,
    device,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Verified push principal ID
     */
    principal: string,
    /**
     * Registered device ID
     */
    device: string,
    requestBody: ManagedRealtimePushRegistration,
  }): CancelablePromise<{
    ok: boolean;
  }> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/devices/{device}',
      path: {
        'slug': slug,
        'id': id,
        'device': device,
      },
      query: {
        'principal': principal,
      },
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
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Remove a push registration and cancel its queued work.
   * Removes a device registration under the retained-history preview gate and cancels its queued push deliveries.
   * @returns any Device registration removed and pending work canceled.
   * @throws ApiError
   */
  public static unregisterManagedRealtimePushDevice({
    slug,
    id,
    principal,
    device,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    /**
     * Verified push principal ID
     */
    principal: string,
    /**
     * Registered device ID
     */
    device: string,
  }): CancelablePromise<{
    ok: boolean;
  }> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/push/devices/{device}',
      path: {
        'slug': slug,
        'id': id,
        'device': device,
      },
      query: {
        'principal': principal,
      },
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
        503: `code: capacity_unavailable — no host headroom.
        Resource increases can return service_recovery_capacity_unavailable
        when enabled bare-metal service protection needs more recovery headroom.
        `,
      },
    });
  }
  /**
   * Update a retained channel message
   * @returns ManagedRealtimeMessageMutationResponse Update a retained channel message result.
   * @throws ApiError
   */
  public static updateManagedRealtimeChannelMessage({
    slug,
    id,
    channel,
    messageId,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Notification message ID
     */
    messageId: string,
    requestBody: ManagedRealtimeMessageMutationRequest,
  }): CancelablePromise<ManagedRealtimeMessageMutationResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/retained-messages/{message_id}',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'message_id': messageId,
      },
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
      },
    });
  }
  /**
   * Delete a retained channel message
   * @returns ManagedRealtimeMessageMutationResponse Delete a retained channel message result.
   * @throws ApiError
   */
  public static deleteManagedRealtimeChannelMessage({
    slug,
    id,
    channel,
    messageId,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Notification message ID
     */
    messageId: string,
    requestBody: ManagedRealtimeMessageMutationRequest,
  }): CancelablePromise<ManagedRealtimeMessageMutationResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/retained-messages/{message_id}',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
        'message_id': messageId,
      },
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
      },
    });
  }
  /**
   * Read principal channel read progress
   * @returns ManagedRealtimeReadProgressResponse Read principal channel read progress result.
   * @throws ApiError
   */
  public static getManagedRealtimeChannelReadProgress({
    slug,
    id,
    channel,
    principal,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Verified principal ID
     */
    principal: string,
  }): CancelablePromise<ManagedRealtimeReadProgressResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/read-progress',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      query: {
        'principal': principal,
      },
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
      },
    });
  }
  /**
   * Advance principal channel read progress
   * @returns ManagedRealtimeReadProgressResponse Advance principal channel read progress result.
   * @throws ApiError
   */
  public static advanceManagedRealtimeChannelReadProgress({
    slug,
    id,
    channel,
    principal,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Channel name
     */
    channel: string,
    /**
     * Verified principal ID
     */
    principal: string,
    requestBody: ManagedRealtimeReadProgressRequest,
  }): CancelablePromise<ManagedRealtimeReadProgressResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/read-progress',
      path: {
        'slug': slug,
        'id': id,
        'channel': channel,
      },
      query: {
        'principal': principal,
      },
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
      },
    });
  }
  /**
   * Update a retained inbox message
   * @returns ManagedRealtimeMessageMutationResponse Update a retained inbox message result.
   * @throws ApiError
   */
  public static updateManagedRealtimeInboxMessage({
    slug,
    id,
    principal,
    messageId,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Verified principal ID
     */
    principal: string,
    /**
     * Notification message ID
     */
    messageId: string,
    requestBody: ManagedRealtimeMessageMutationRequest,
  }): CancelablePromise<ManagedRealtimeMessageMutationResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/principals/inbox/{message_id}',
      path: {
        'slug': slug,
        'id': id,
        'message_id': messageId,
      },
      query: {
        'principal': principal,
      },
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
      },
    });
  }
  /**
   * Delete a retained inbox message
   * @returns ManagedRealtimeMessageMutationResponse Delete a retained inbox message result.
   * @throws ApiError
   */
  public static deleteManagedRealtimeInboxMessage({
    slug,
    id,
    principal,
    messageId,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Verified principal ID
     */
    principal: string,
    /**
     * Notification message ID
     */
    messageId: string,
    requestBody: ManagedRealtimeMessageMutationRequest,
  }): CancelablePromise<ManagedRealtimeMessageMutationResponse> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/principals/inbox/{message_id}',
      path: {
        'slug': slug,
        'id': id,
        'message_id': messageId,
      },
      query: {
        'principal': principal,
      },
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
      },
    });
  }
  /**
   * Read principal inbox read progress
   * @returns ManagedRealtimeReadProgressResponse Read principal inbox read progress result.
   * @throws ApiError
   */
  public static getManagedRealtimeInboxReadProgress({
    slug,
    id,
    principal,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Verified principal ID
     */
    principal: string,
  }): CancelablePromise<ManagedRealtimeReadProgressResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/principals/inbox/read-progress',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'principal': principal,
      },
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
      },
    });
  }
  /**
   * Advance principal inbox read progress
   * @returns ManagedRealtimeReadProgressResponse Advance principal inbox read progress result.
   * @throws ApiError
   */
  public static advanceManagedRealtimeInboxReadProgress({
    slug,
    id,
    principal,
    requestBody,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Verified principal ID
     */
    principal: string,
    requestBody: ManagedRealtimeReadProgressRequest,
  }): CancelablePromise<ManagedRealtimeReadProgressResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/principals/inbox/read-progress',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'principal': principal,
      },
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
      },
    });
  }
  /**
   * Read retained principal inbox messages
   * @returns ManagedRealtimeInboxResponse Read retained principal inbox messages result.
   * @throws ApiError
   */
  public static readManagedRealtimeInbox({
    slug,
    id,
    principal,
    after,
    limit = 100,
    consumer,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Verified principal ID
     */
    principal: string,
    /**
     * Exclusive inbox sequence cursor
     */
    after?: number,
    /**
     * Maximum inbox messages per page
     */
    limit?: number,
    /**
     * Durable inbox consumer name
     */
    consumer?: string,
  }): CancelablePromise<ManagedRealtimeInboxResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/principals/inbox',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'principal': principal,
        'after': after,
        'limit': limit,
        'consumer': consumer,
      },
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
      },
    });
  }
  /**
   * Get principal message receipt state
   * @returns ManagedRealtimePrincipalReceiptResponse Get principal message receipt state result.
   * @throws ApiError
   */
  public static getManagedRealtimePrincipalReceipt({
    slug,
    id,
    messageId,
  }: {
    /**
     * Application slug
     */
    slug: string,
    /**
     * Realtime endpoint ID
     */
    id: string,
    /**
     * Notification message ID
     */
    messageId: string,
  }): CancelablePromise<ManagedRealtimePrincipalReceiptResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/realtime/endpoints/{id}/principals/messages/{message_id}/receipt',
      path: {
        'slug': slug,
        'id': id,
        'message_id': messageId,
      },
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
      },
    });
  }
}
