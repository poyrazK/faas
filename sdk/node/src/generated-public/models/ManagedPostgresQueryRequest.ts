/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One read-only SQL statement.
 */
export type ManagedPostgresQueryRequest = {
  /**
   * One statement; several are refused.
   */
  sql: string;
  /**
   * Rows to return; default 100.
   */
  max_rows?: number;
};

