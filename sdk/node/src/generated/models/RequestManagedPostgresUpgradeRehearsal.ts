/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type RequestManagedPostgresUpgradeRehearsal = {
  /**
   * Reuse when retrying a request with the same source and target major.
   */
  request_id: string;
  /**
   * Target major version.
   */
  postgres_major: number;
};

