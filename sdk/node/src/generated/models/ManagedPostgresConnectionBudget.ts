/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConnectionBudgetCapacity } from './ConnectionBudgetCapacity.js';
import type { ConnectionBudgetDemand } from './ConnectionBudgetDemand.js';
import type { ConnectionBudgetInventory } from './ConnectionBudgetInventory.js';
/**
 * Capacity evidence and demand. Canonical pool planning remains unknown without complete evidence. Qualified mirrors enforce shared reservations and login ceilings; their modeled peak includes all reservations sharing physical limits. Runtime pool estimate fields are zero in that model.
 */
export type ManagedPostgresConnectionBudget = {
  version: 1;
  database_id: string;
  database_name: string;
  state: 'unknown' | 'within_budget' | 'over_budget';
  checked_at: string;
  expires_at: string;
  enforced: boolean;
  model: 'canonical_runtime_pools' | 'qualified_shared_connection_limits';
  inventory: ConnectionBudgetInventory;
  demand: ConnectionBudgetDemand;
  capacity: ConnectionBudgetCapacity;
  reasons: Array<string>;
};

