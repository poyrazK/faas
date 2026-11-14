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
   * The plan's monthly compute allowance in compute-unit seconds, pooled across the account's databases.
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
  /**
   * True while the account runs its databases past the allowance, up to the ceiling.
   */
  overage_enabled?: boolean;
  /**
   * When the compute cap suspended the account's databases this month; present while compute_cap_state is suspended.
   */
  compute_suspended_at?: string;
  /**
   * Each database's own monthly egress allowance, 500 GB (10^9 bytes) as Neon includes per project. Database egress does not count toward the plan's app egress allowance. Absent on a plan without databases.
   */
  egress_allowance_bytes?: number;
  /**
   * Deprecated and always omitted. Apps no longer share the databases' egress allowance.
   * @deprecated
   */
  app_egress_bytes?: number;
  /**
   * The state of the database that sent the most egress this month: healthy; warning at 80% of its hold; overage past its allowance while the account's egress overage election bills database egress (at Neon's price plus 5%, up to twice the allowance); held once it reaches its allowance (twice it with overage billed) and the platform enforces caps, until the month ends. Only that database is held.
   */
  egress_state?: 'healthy' | 'warning' | 'overage' | 'held';
  /**
   * Set while every database of the account is held, with the same reason each database reports. Egress holds one database at a time, so it is reported on that database, not here.
   */
  hold?: 'compute_ceiling' | 'egress' | 'inactive';
};

