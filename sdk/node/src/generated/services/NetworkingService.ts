/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppPrivateNetworkAttachmentRequest } from '../models/AppPrivateNetworkAttachmentRequest.js';
import type { AppPrivateNetworkAttachmentResponse } from '../models/AppPrivateNetworkAttachmentResponse.js';
import type { CreatePrivateNetworkPeeringRequest } from '../models/CreatePrivateNetworkPeeringRequest.js';
import type { CreatePrivateNetworkRequest } from '../models/CreatePrivateNetworkRequest.js';
import type { PrivateNetwork } from '../models/PrivateNetwork.js';
import type { PrivateNetworkListResponse } from '../models/PrivateNetworkListResponse.js';
import type { PrivateNetworkMembersResponse } from '../models/PrivateNetworkMembersResponse.js';
import type { PrivateNetworkPeering } from '../models/PrivateNetworkPeering.js';
import type { PrivateNetworkPeeringListResponse } from '../models/PrivateNetworkPeeringListResponse.js';
import type { UpdatePrivateNetworkPolicyRequest } from '../models/UpdatePrivateNetworkPolicyRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class NetworkingService {
  /**
   * List Gregale-owned private networks.
   * Returns the caller's private-network definitions. This surface is
   * dark-launched with FAAS_PRIVATE_NETWORK_FABRIC_ENABLED and is
   * independent of DigitalOcean or any other provider API.
   *
   * @returns PrivateNetworkListResponse Account-scoped private networks.
   * @throws ApiError
   */
  public static listPrivateNetworks(): CancelablePromise<PrivateNetworkListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/networks',
      errors: {
        401: `code: unauthorized`,
        503: `Gregale-owned network fabric is disabled for this account-scoped listing.`,
      },
    });
  }
  /**
   * Create a Gregale-owned private network.
   * Creates an IPv4 RFC1918 /16-/28 address space owned by Gregale.
   * The region is a Gregale placement label, not a DigitalOcean region
   * identifier. Host bridge/overlay activation remains asynchronous and
   * app attachments stay fail-closed until ready.
   *
   * @returns PrivateNetwork Network definition created.
   * @throws ApiError
   */
  public static createPrivateNetwork({
    requestBody,
  }: {
    requestBody: CreatePrivateNetworkRequest,
  }): CancelablePromise<PrivateNetwork> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/networks',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `The account plan does not include private networking.`,
        409: `Name or regional CIDR conflicts with an existing network.`,
        503: `Gregale-owned network fabric is disabled for network creation.`,
      },
    });
  }
  /**
   * Read a Gregale-owned private network.
   * @returns PrivateNetwork Private network definition.
   * @throws ApiError
   */
  public static getPrivateNetwork({
    id,
  }: {
    /**
     * Stable Gregale network identifier returned at creation.
     */
    id: string,
  }): CancelablePromise<PrivateNetwork> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/networks/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `Gregale-owned network fabric is disabled for this network read.`,
      },
    });
  }
  /**
   * Delete a private network with no app attachments.
   * @returns void
   * @throws ApiError
   */
  public static deletePrivateNetwork({
    id,
  }: {
    /**
     * Stable Gregale network identifier returned at creation.
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/networks/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `Network still has an app attachment.`,
        503: `Gregale-owned network fabric is disabled for this network deletion.`,
      },
    });
  }
  /**
   * List members and address capacity for a private network.
   * Returns the account-scoped stable address reservations for this
   * Gregale-owned network. Capacity excludes the network address, gateway,
   * and broadcast address; this endpoint is inventory only and does not
   * probe workloads or call DigitalOcean APIs.
   *
   * @returns PrivateNetworkMembersResponse Private-network member inventory.
   * @throws ApiError
   */
  public static listPrivateNetworkMembers({
    id,
  }: {
    /**
     * Stable Gregale network identifier whose members are listed.
     */
    id: string,
  }): CancelablePromise<PrivateNetworkMembersResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/networks/{id}/members',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `Gregale-owned network fabric is disabled for this network inventory read.`,
      },
    });
  }
  /**
   * List peerings for a private network.
   * Returns account-scoped peering intents involving this network. A
   * pending or error peering remains fail-closed until both route domains
   * converge; this surface does not call DigitalOcean APIs.
   *
   * @returns PrivateNetworkPeeringListResponse Private-network peerings.
   * @throws ApiError
   */
  public static listPrivateNetworkPeerings({
    id,
  }: {
    /**
     * Stable Gregale network identifier whose peerings are listed.
     */
    id: string,
  }): CancelablePromise<PrivateNetworkPeeringListResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/networks/{id}/peerings',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `Gregale-owned network fabric is disabled.`,
      },
    });
  }
  /**
   * Request peering with another private network.
   * Creates a pending, symmetric peering between two Gregale-owned
   * networks in the same account and region. Networks must have
   * non-overlapping IPv4 CIDRs. Route activation is asynchronous and
   * remains fail-closed until the node fabric converges.
   *
   * @returns PrivateNetworkPeering Peering intent accepted.
   * @throws ApiError
   */
  public static createPrivateNetworkPeering({
    id,
    requestBody,
  }: {
    /**
     * Stable Gregale network identifier whose peerings are listed.
     */
    id: string,
    requestBody: CreatePrivateNetworkPeeringRequest,
  }): CancelablePromise<PrivateNetworkPeering> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/networks/{id}/peerings',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `Private networking is unavailable on this account plan for peering.`,
        404: `code: not_found`,
        409: `The two networks are already peered.`,
        503: `The Gregale-owned fabric is disabled for peering creation.`,
      },
    });
  }
  /**
   * Read a private-network peering.
   * @returns PrivateNetworkPeering Private-network peering.
   * @throws ApiError
   */
  public static getPrivateNetworkPeering({
    id,
    peerId,
  }: {
    /**
     * Stable Gregale network identifier.
     */
    id: string,
    /**
     * Stable peering identifier returned when the intent is created.
     */
    peerId: string,
  }): CancelablePromise<PrivateNetworkPeering> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/networks/{id}/peerings/{peer_id}',
      path: {
        'id': id,
        'peer_id': peerId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `The Gregale-owned fabric is disabled for peering reads.`,
      },
    });
  }
  /**
   * Remove a private-network peering intent.
   * @returns void
   * @throws ApiError
   */
  public static deletePrivateNetworkPeering({
    id,
    peerId,
  }: {
    /**
     * Stable Gregale network identifier.
     */
    id: string,
    /**
     * Stable peering identifier returned when the intent is created.
     */
    peerId: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/networks/{id}/peerings/{peer_id}',
      path: {
        'id': id,
        'peer_id': peerId,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        503: `The Gregale-owned fabric is disabled for peering deletion.`,
      },
    });
  }
  /**
   * Replace a private network's reusable firewall policy.
   * Replaces the network-level IPv4 CIDR allowlist and optional
   * protocol/port rules used by every attached workload. Empty lists
   * preserve the legacy allow-all behavior; non-empty rules are enforced
   * fail-closed. App-level policy may further restrict destinations but
   * cannot broaden the network baseline.
   *
   * @returns PrivateNetwork Updated private network definition.
   * @throws ApiError
   */
  public static updatePrivateNetworkPolicy({
    id,
    requestBody,
  }: {
    /**
     * Network identifier whose reusable firewall policy is being replaced.
     */
    id: string,
    requestBody: UpdatePrivateNetworkPolicyRequest,
  }): CancelablePromise<PrivateNetwork> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/networks/{id}/policy',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `Private networking plan required for policy updates.`,
        404: `code: not_found`,
        503: `Gregale-owned network fabric is disabled for this policy update.`,
      },
    });
  }
  /**
   * Read the app private-network attachment intent.
   * Returns feature and plan capability metadata plus the current
   * attachment, or `attachment=null` when none is configured. A pending
   * or error attachment never admits private traffic.
   *
   * @returns AppPrivateNetworkAttachmentResponse Current private-network attachment state.
   * @throws ApiError
   */
  public static getAppPrivateNetworkAttachment({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<AppPrivateNetworkAttachmentResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/network/private',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: app_not_found — slug does not exist for the authenticated account.`,
        503: `The cluster operator has not enabled private-network attachments for this request.`,
      },
    });
  }
  /**
   * Request a private-network attachment.
   * Replaces the app's provider-neutral attachment intent. Pro and Scale
   * plans may request up to their plan CIDR cap (16 and 64 respectively).
   * Network ID and region use the lowercase provider-neutral identifier
   * grammar. CIDRs must be non-default IPv4 RFC1918 ranges, must not
   * overlap each other, and must not overlap Gregale's reserved ranges.
   * The response is `202 Accepted` with `status=pending`; traffic remains
   * blocked until a connector advances the row to `ready`.
   *
   * @returns AppPrivateNetworkAttachmentResponse Attachment intent accepted for asynchronous reconciliation.
   * @throws ApiError
   */
  public static setAppPrivateNetworkAttachment({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: AppPrivateNetworkAttachmentRequest,
  }): CancelablePromise<AppPrivateNetworkAttachmentResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/network/private',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `The account plan does not include private-network attachments.`,
        404: `code: app_not_found — slug does not exist for the authenticated account.`,
        503: `Private-network attachment routes are disabled on this cluster.`,
      },
    });
  }
  /**
   * Remove the app private-network attachment intent.
   * @returns void
   * @throws ApiError
   */
  public static clearAppPrivateNetworkAttachment({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/network/private',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: app_not_found — slug does not exist for the authenticated account.`,
        503: `The cluster has not enabled private-network attachments.`,
      },
    });
  }
}
