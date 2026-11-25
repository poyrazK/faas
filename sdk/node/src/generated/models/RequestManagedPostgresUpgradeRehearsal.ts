/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Target PostgreSQL major and request identifier for a private upgrade rehearsal.
 */
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

