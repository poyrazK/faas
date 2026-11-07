/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Empty success body. Side effects: mfa_secret_encrypted,
 * mfa_recovery_codes_hash, and mfa_enrolled_at are all NULL.
 * mfa_required is left as-is so an explicit policy remains in
 * force after disable. The browser cookie is refreshed with the
 * current enrollment requirement and its recent-verification stamp cleared.
 *
 */
export type MFADisableResponse = Record<string, any>;
