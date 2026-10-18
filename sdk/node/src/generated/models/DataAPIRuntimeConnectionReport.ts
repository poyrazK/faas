/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Private versioned runtime configuration report. Instance, deployment, node and database placement authority are resolved from workload identity and the catalog, never accepted from this body. observed_at bounds replay; unknown invalidates the previous observation.
 */
export type DataAPIRuntimeConnectionReport = {
  version: 1;
  binding_id: string;
  /**
   * SHA-256 of backend_fingerprint, a colon and the frozen data_resource_id, from operator configuration.
   */
  database_identity: string;
  database_generation: number;
  credential_generation: number;
  role: string;
  state: 'observed' | 'unknown';
  runtime_model: 'postgrest-14.3-pools-v1';
  engine_connections: number;
  refresh_connections: number;
  observed_at: string;
};

