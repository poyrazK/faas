/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FinancialBudgetScope } from './FinancialBudgetScope.js';
/**
 * Customer budget intent; activation and enforcement are separately acknowledged.
 */
export type FinancialBudgetSpec = {
  /**
   * Nonblank name bounded to 128 UTF-8 bytes.
   */
  name: string;
  scope: FinancialBudgetScope;
  currency: 'EUR';
  /**
   * Sorted meter names; strict mode covers compute only.
   */
  meters: Array<'compute' | 'egress'>;
  /**
   * Net usage after the shared account allowance or gross usage before it; strict resource scopes require gross usage.
   */
  basis: 'net_usage' | 'gross_usage';
  limit_millicents: number;
  /**
   * Increasing nonnegative thresholds at or below the limit.
   */
  notify_millicents: Array<number>;
  mode: 'monitored' | 'strict';
  action: 'notify' | 'reject_traffic' | 'suspend_background' | 'stop_previews' | 'suspend_workloads';
  /**
   * Notify uses zero; stopping targets drain before the deadline.
   */
  drain_seconds: number;
  resume_rule: 'manual' | 'next_period';
  enabled: boolean;
};

