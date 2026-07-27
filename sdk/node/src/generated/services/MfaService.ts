/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MFAConfirmRequest } from '../models/MFAConfirmRequest.js';
import type { MFAConfirmResponse } from '../models/MFAConfirmResponse.js';
import type { MFADisableRequest } from '../models/MFADisableRequest.js';
import type { MFADisableResponse } from '../models/MFADisableResponse.js';
import type { MFAEnrollResponse } from '../models/MFAEnrollResponse.js';
import type { MFARecoverRequest } from '../models/MFARecoverRequest.js';
import type { MFARecoverResponse } from '../models/MFARecoverResponse.js';
import type { MFAVerifyRequest } from '../models/MFAVerifyRequest.js';
import type { MFAVerifyResponse } from '../models/MFAVerifyResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class MfaService {
  /**
   * Start MFA enrollment.
   * Returns the TOTP secret, QR PNG, otpauth URL, and 10
   * recovery codes exactly once. The plaintext secret is
   * shown only here; the server stores a sealed copy. Call
   * `/v1/account/mfa/confirm` with the customer's first
   * 6-digit code to commit the enrollment.
   *
   * @returns MFAEnrollResponse Enrollment ready. Render the QR + recovery codes to the customer.
   * @throws ApiError
   */
  public static mfaEnroll(): CancelablePromise<MFAEnrollResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/mfa/enroll',
      errors: {
        401: `code: unauthorized`,
        409: `Already enrolled. Call /v1/account/mfa/disable first.`,
        503: `MFA unavailable — host age key not loaded.`,
      },
    });
  }
  /**
   * Finish MFA enrollment with the first TOTP code.
   * Verifies the 6-digit code against the sealed secret
   * and stamps mfa_enrolled_at. Re-issues the session
   * cookie without the mfa_pending flag.
   *
   * @returns MFAConfirmResponse Enrolled.
   * @throws ApiError
   */
  public static mfaConfirm({
    requestBody,
  }: {
    requestBody: MFAConfirmRequest,
  }): CancelablePromise<MFAConfirmResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/mfa/confirm',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `Invalid TOTP code.`,
        404: `No in-flight enrollment. Call /enroll first.`,
      },
    });
  }
  /**
   * Step up an mfa_pending session.
   * For already-enrolled customers whose session cookie is
   * mfa_pending. Same TOTP check as /confirm, but does NOT
   * stamp mfa_enrolled_at. Re-issues the cookie without
   * mfa_pending so subsequent requests pass requireMFA.
   *
   * @returns MFAVerifyResponse Stepped up.
   * @throws ApiError
   */
  public static mfaVerify({
    requestBody,
  }: {
    requestBody: MFAVerifyRequest,
  }): CancelablePromise<MFAVerifyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/mfa/verify',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `Invalid code.`,
      },
    });
  }
  /**
   * Burn a recovery code to regain access.
   * Use when the customer's TOTP device is lost. Removes
   * one matching SHA-256 hash from the stored set; if the
   * customer burns the last code, /recover still works but
   * /disable via recovery_code no longer does.
   *
   * @returns MFARecoverResponse Recovered.
   * @throws ApiError
   */
  public static mfaRecover({
    requestBody,
  }: {
    requestBody: MFARecoverRequest,
  }): CancelablePromise<MFARecoverResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/mfa/recover',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `No matching recovery code.`,
      },
    });
  }
  /**
   * Opt out of MFA.
   * Clears the TOTP secret, recovery codes, and
   * mfa_enrolled_at. Body must include exactly one of
   * `password` (re-verified against account_passwords)
   * or `recovery_code` (consumed). Leaves mfa_required
   * untouched so the plan-upgrade / 2nd-deploy
   * chokepoints can re-arm.
   *
   * @returns MFADisableResponse Disabled.
   * @throws ApiError
   */
  public static mfaDisable({
    requestBody,
  }: {
    requestBody: MFADisableRequest,
  }): CancelablePromise<MFADisableResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/account/mfa/disable',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Both or neither of password/recovery_code set.`,
        401: `Invalid credentials or recovery code.`,
      },
    });
  }
}
