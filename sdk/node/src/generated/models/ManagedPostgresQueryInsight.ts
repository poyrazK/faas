/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Aggregate execution statistics for one normalized statement.
 */
export type ManagedPostgresQueryInsight = {
  /**
   * PostgreSQL query ID within the indicated boot.
   */
  query_id: string;
  boot_at: string;
  calls: number;
  total_execution_ms: number;
  mean_execution_ms: number;
};

