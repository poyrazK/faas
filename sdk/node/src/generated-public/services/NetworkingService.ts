/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ServiceCallerJWKSet } from '../models/ServiceCallerJWKSet.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
export class NetworkingService {
  /**
   * Read the public keys used to verify service-caller assertions.
   * Unauthenticated, rate-limited JWKS containing only public Ed25519 keys.
   * Keys retired during node rotation remain listed for the 30-second
   * assertion lifetime. Cache responses only briefly and refresh on an
   * unknown `kid` before rejecting an otherwise valid assertion.
   *
   * @returns ServiceCallerJWKSet Current and rotation-grace verification keys.
   * @throws ApiError
   */
  public static getServiceCallerKeys(): CancelablePromise<ServiceCallerJWKSet> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/service-caller-keys',
      errors: {
        429: `Source-IP request limit exceeded.`,
        500: `The public verification key set could not be read safely.`,
      },
    });
  }
}
