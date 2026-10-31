/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for /disable. Exactly one of `password` or
 * `recovery_code` is required — both empty and both set are
 * rejected with 400 CodeValidation. Password is verified
 * against the existing `account_passwords.hash`; the
 * recovery code is consumed (one-time).
 *
 */
export type MFADisableRequest = {
  password?: string;
  recovery_code?: string;
  /**
   * Dashboard CSRF token for the disable mutation.
   */
  csrf_token?: string;
};

