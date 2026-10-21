/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialEgressBalance } from './FinancialEgressBalance.js';
/**
 * Conservative admission ledger. App/job budgets cap gross compute. Account budgets share included quantities; with meters compute and egress the monetary totals sum both meters under one paid limit and egress shows its contribution. Byte credits include framing and unused credits commit after physical retirement; these upper bounds can exceed invoice charges. Expired funding stays reserved until physical retirement. Subscriptions, storage, builds, object-storage delivery and taxes are excluded. Independent job attempts share account funding with apps.
 */
export type FinancialComputeBalance = {
  egress?: FinancialEgressBalance;
  initialized: boolean;
  app_id?: string;
  job_id?: string;
  /**
   * The capped project of a strict project budget (ADR-1750, amendment 1): one gross book shared by every app in the project. Jobs are not covered.
   */
  project_id?: string;
  account_id?: string;
  scope_kind?: 'account' | 'app' | 'job' | 'project';
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

