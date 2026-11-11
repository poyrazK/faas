/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Empty success body. Side effects: the matching hash is
 * removed from `mfa_recovery_codes_hash`; the session cookie
 * is re-issued without `mfa_pending` and with recent MFA verification.
 * The last remaining code returns `mfa_last_recovery_code`; use it
 * to reset the authenticator instead, then enroll a replacement.
 *
 */
export type MFARecoverResponse = Record<string, any>;
