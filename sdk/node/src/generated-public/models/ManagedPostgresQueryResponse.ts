/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The rows one read-only statement read.
 */
export type ManagedPostgresQueryResponse = {
  database_id: string;
  columns: Array<{
    name: string;
    /**
     * PostgreSQL type name, or oid:N for a type the server did not name.
     */
    type: string;
  }>;
  rows: Array<Array<string | null>>;
  row_count: number;
  /**
   * True when the row or 1 MiB cap stopped reading before the result ended.
   */
  truncated: boolean;
  duration_ms: number;
};

