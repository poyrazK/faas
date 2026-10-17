/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Conservative compute admission ledger. App scopes use gross compute; account scopes share included MB-seconds and cap net paid compute. Commitments may exceed invoice charges. Expired funding remains reserved until physical retirement. Subscriptions, storage, egress, builds and taxes are excluded. Account strict activation requires no independent jobs.
 */
export type FinancialComputeBalance = {
  initialized: boolean;
  app_id?: string;
  account_id?: string;
  scope_kind?: 'account' | 'app';
  basis?: 'net_usage' | 'gross_usage';
  allowance_granted_mb_seconds?: number;
  allowance_committed_mb_seconds?: number;
  allowance_reserved_mb_seconds?: number;
  allowance_remaining_mb_seconds?: number;
  period_start: string;
  period_end: string;
  committed_millicents: number;
  reserved_millicents: number;
  remaining_millicents: number;
  unconfirmed_leases: number;
  held: boolean;
  observed_at: string;
};

