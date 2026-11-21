/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RunBudgetKind } from './RunBudgetKind.js';
/**
 * Recorded customer or task budget threshold event.
 */
export type RunBudgetAlert = {
  event_id: string;
  kind: RunBudgetKind;
  ref: string;
  day: string;
  metric: 'concurrent' | 'invocations' | 'compute_mb_ms';
  threshold_percent: 80 | 100;
  limit: number;
  observed: number;
  created_at: string;
};

