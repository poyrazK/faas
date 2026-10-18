/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConnectionBudgetCapacity } from './ConnectionBudgetCapacity.js';
import type { ConnectionBudgetDemand } from './ConnectionBudgetDemand.js';
import type { ConnectionBudgetInventory } from './ConnectionBudgetInventory.js';
/**
 * Canonical pool planning, not live SQL usage or reservation. A comparison requires complete verified capacity and consumer evidence; current adapters report unknown.
 */
export type ManagedPostgresConnectionBudget = {
  version: 1;
  database_id: string;
  database_name: string;
  state: 'unknown' | 'within_budget' | 'over_budget';
  checked_at: string;
  expires_at: string;
  enforced: boolean;
  model: 'canonical_runtime_pools';
  inventory: ConnectionBudgetInventory;
  demand: ConnectionBudgetDemand;
  capacity: ConnectionBudgetCapacity;
  reasons: Array<string>;
};

