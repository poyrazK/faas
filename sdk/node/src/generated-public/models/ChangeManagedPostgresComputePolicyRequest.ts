/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Canonical UUID request for a scale-to-zero change on an existing managed PostgreSQL database.
 */
export type ChangeManagedPostgresComputePolicyRequest = {
  /**
   * Canonical nonzero UUID for this policy intent; replay with the same database and scale-to-zero setting after uncertain responses.
   */
  request_id: string;
  scale_to_zero: boolean;
};

