/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Queue inspection of the current credential, optionally including a ready, empty managed mirror.
 */
export type RequestDataConnectionCheck = {
  /**
   * Select snapshot inspection for read-only scheduled imports; omit for continuous replication.
   */
  mode?: 'snapshot';
  /**
   * Exact public table names to inspect, bypassing the default discovery page. Mutually exclusive with search.
   */
  tables?: Array<string>;
  /**
   * Discover names containing this literal text before applying the metadata limit. Mutually exclusive with tables.
   */
  search?: string;
  expected_revision: number;
  database_id?: string;
};

