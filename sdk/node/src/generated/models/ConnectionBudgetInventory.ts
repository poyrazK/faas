/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Deduplicated app/scope potential consumers; counts are not live SQL sessions.
 */
export type ConnectionBudgetInventory = {
  bindings: number;
  data_api_consumers: number;
  auth_consumers: number;
  migration_bindings: number;
  other_bindings: number;
  resident_instances: number;
  parked_instances: number;
  auxiliary_instances: number;
  active_tasks: number;
  rotation_pending: number;
  overlap_consumers: number;
  stale_node_instances: number;
};

