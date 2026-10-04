/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable regional stream delegation; the plaintext token never reaches registration.
 */
export type PutRegionalReleaseGrantRequest = {
  /**
   * Global image release stream UUID.
   */
  global_app_id: string;
  /**
   * SHA-256 of the directory-minted fp_release_ bearer.
   */
  token_sha256: string;
  /**
   * Fixed expiry within seven days; canonicalized to UTC microseconds.
   */
  expires_at: string;
};

