/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for /verify — same 6-digit code as /confirm. The
 * account is already enrolled; the verify path does NOT re-
 * stamp enrolled_at, only re-issues the cookie without
 * mfa_pending.
 *
 */
export type MFAVerifyRequest = {
  totp: string;
  /**
   * Dashboard CSRF token (absorbed from the body for
   * parity with /confirm; /verify is not token-gated
   * because the TOTP code itself IS the second factor).
   *
   */
  csrf_token?: string;
};

