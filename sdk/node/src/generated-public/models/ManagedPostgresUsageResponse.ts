/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Customer-safe current UTC-month managed PostgreSQL usage and guardrail state.
 */
export type ManagedPostgresUsageResponse = {
  period_start: string;
  observed_at?: string | null;
  policy_enabled: boolean;
  fresh: boolean;
  guardrail_state: 'disabled' | 'healthy' | 'stale' | 'reached';
  ready_databases: number;
  database_limit: number;
  storage_limit_bytes: number;
  compute_unit_seconds: number;
  storage_byte_seconds: number;
  storage_byte_seconds_limit: number;
  storage_byte_seconds_remaining: number;
  history_byte_seconds: number;
  egress_bytes: number;
};

