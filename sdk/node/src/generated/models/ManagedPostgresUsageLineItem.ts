/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Normalized operator-only managed PostgreSQL ledger line.
 */
export type ManagedPostgresUsageLineItem = {
  code: 'managed_postgres.compute' | 'managed_postgres.storage' | 'managed_postgres.restore_history' | 'managed_postgres.egress';
  meter: string;
  unit: string;
  quantity: number;
  cost_millicents: number;
};

