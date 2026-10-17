/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresConnectionBudget } from './ManagedPostgresConnectionBudget.js';
/**
 * Read-only planning snapshots. Not applicable means the app has no visible Data API binding.
 */
export type DataAPIConnectionBudgets = {
  version: 1;
  app: string;
  state: 'not_applicable' | 'unknown' | 'within_budget' | 'over_budget';
  truncated: boolean;
  databases: Array<ManagedPostgresConnectionBudget>;
};

