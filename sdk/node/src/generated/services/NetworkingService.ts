/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppPrivateNetworkAttachmentRequest } from '../models/AppPrivateNetworkAttachmentRequest.js';
import type { AppPrivateNetworkAttachmentResponse } from '../models/AppPrivateNetworkAttachmentResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class NetworkingService {
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
