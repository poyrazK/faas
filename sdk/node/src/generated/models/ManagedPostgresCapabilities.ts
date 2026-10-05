/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Versioned PostgreSQL support for the default backend in a region, intersected with customer plan limits. Provider identities, costs, endpoints and credentials are excluded. Capabilities remain visible while the rollout gate is closed.
 */
export type ManagedPostgresCapabilities = {
  contract_version: number;
  region: string;
  provisioning_enabled: boolean;
  database_limit: number;
  postgres_majors: Array<number>;
  service_classes: Array<'development' | 'burstable' | 'production'>;
  availability: Array<'single_zone' | 'high_availability'>;
  credential_access: Array<'read_write' | 'read_only' | 'migration'>;
  scale_to_zero: boolean;
  always_on: boolean;
  pooled_connections: boolean;
  point_in_time_restore: boolean;
  storage_limit_bytes: number;
  restore_window_seconds: number;
};

