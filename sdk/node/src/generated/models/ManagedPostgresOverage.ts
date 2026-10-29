/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An account's managed PostgreSQL compute overage setting.
 */
export type ManagedPostgresOverage = {
  enabled: boolean;
  /**
   * The plan's monthly compute allowance in compute-unit seconds.
   */
  compute_allowance_unit_seconds: number;
  /**
   * Where running databases are suspended before the operator's usage cap; twice the allowance with overage on.
   */
  compute_ceiling_unit_seconds: number;
};

