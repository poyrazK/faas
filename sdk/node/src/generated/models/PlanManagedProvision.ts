/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The managed PostgreSQL database a project apply creates or reuses for a Compose postgres service, and the service keys it binds (ADR-20261025111824).
 */
export type PlanManagedProvision = {
  database: string;
  /**
   * True when a database with this name already exists and is reused.
   */
  existing?: boolean;
  /**
   * The database's PostgreSQL major: the Compose image's, or the nearest newer one the platform offers.
   */
  postgres_major?: number;
  bindings: Array<{
    workload: string;
    env_key: string;
  }>;
};

