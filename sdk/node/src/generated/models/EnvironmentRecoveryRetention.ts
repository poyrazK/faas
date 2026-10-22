/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit artifact retention; count, age and byte limits apply together. Removing the policy or detaching releases its protection. No snapshot or trust transfer.
 */
export type EnvironmentRecoveryRetention = {
  /**
   * Maximum newest completed serving releases, including the active release when served.
   */
  releases: number;
  /**
   * Maximum age measured from original serving completion; observations never renew it.
   */
  seconds: number;
  /**
   * Conservative per-release unique layer bytes budget; account policy reservations are limited to 2147483648 bytes.
   */
  max_bytes: number;
};

