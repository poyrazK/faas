/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Response from `POST /v1/apps/{slug}/secrets/{key}/rotate`. The
 * `kid` is the age-1... recipient string of the host identity that
 * sealed the new envelope (ADR-089 D4); `rotated_at` is
 * RFC3339Nano so two rotates in the same second produce distinct
 * timestamps. Empty `kid` means the row was rotated but the kid
 * was not stampable (rare — happens only if apid started without
 * host.age.pub, which the handler 503s for instead).
 *
 */
export type RotateAppSecretResponse = {
  key: string;
  rotated_at: string;
  /**
   * age-1... recipient string of the host identity that sealed this row. Empty if kid was not stampable.
   */
  kid?: string;
};

