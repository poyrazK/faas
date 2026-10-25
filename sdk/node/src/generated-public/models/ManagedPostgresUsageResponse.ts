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
  /**
   * The plan's monthly compute allowance in compute-unit seconds, pooled across the account's databases (ADR-20261025094915).
   */
  compute_allowance_unit_seconds?: number;
  /**
   * Compute-unit seconds this month at which running databases are suspended. Absent while the usage ledger is off.
   */
  compute_ceiling_unit_seconds?: number;
  /**
   * healthy; warning at 80% of the allowance; overage past the allowance while the account allows overage; suspended once the ceiling is reached and the platform enforces caps. Suspended databases keep their data and resume next month or after an upgrade.
   */
  compute_cap_state?: 'healthy' | 'warning' | 'overage' | 'suspended';
};

