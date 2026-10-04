/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Cost changes and resulting coverage from preview or an immutable applied import receipt.
 */
export type ManagedPostgresUsageImportResult = {
  import_id: string;
  database_id: string;
  revision: string;
  applied: boolean;
  window_count: number;
  previous_cost_millicents: number;
  imported_cost_millicents: number;
  cost_delta_millicents: number;
  collected_from: string;
  collected_until: string;
  observed_at: string;
};

