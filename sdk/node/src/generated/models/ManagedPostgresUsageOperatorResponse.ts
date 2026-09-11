/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresUsageLineItem } from './ManagedPostgresUsageLineItem.js';
/**
 * Operator-only managed PostgreSQL usage, effective ceilings, and internal COGS line items.
 */
export type ManagedPostgresUsageOperatorResponse = {
  account_id: string;
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
  cost_millicents: number;
  max_monthly_cost_millicents: number;
  max_monthly_compute_unit_seconds: number;
  max_monthly_storage_byte_seconds: number;
  max_monthly_history_byte_seconds: number;
  max_monthly_egress_bytes: number;
  line_items: Array<ManagedPostgresUsageLineItem>;
};

