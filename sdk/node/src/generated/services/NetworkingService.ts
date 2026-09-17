/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppPrivateNetworkAttachmentRequest } from '../models/AppPrivateNetworkAttachmentRequest.js';
import type { AppPrivateNetworkAttachmentResponse } from '../models/AppPrivateNetworkAttachmentResponse.js';
import type { CreatePrivateNetworkRequest } from '../models/CreatePrivateNetworkRequest.js';
import type { PrivateNetwork } from '../models/PrivateNetwork.js';
import type { PrivateNetworkListResponse } from '../models/PrivateNetworkListResponse.js';
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
