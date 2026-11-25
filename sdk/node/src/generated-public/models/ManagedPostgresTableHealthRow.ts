/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Storage, dead-row estimates, and vacuum observations for one relation.
 */
export type ManagedPostgresTableHealthRow = {
  schema: string;
  name: string;
  /**
   * Table storage including TOAST and auxiliary forks.
   */
  table_bytes: number;
  /**
   * Space used by indexes attached to the table.
   */
  index_bytes: number;
  /**
   * The PostgreSQL statistics estimate of dead tuples.
   */
  estimated_dead_rows: number;
  /**
   * Estimated rows modified since the last analyze.
   */
  changes_since_analyze: number;
  /**
   * Transaction ID age of the older of the table and its TOAST relation.
   */
  xid_age: number;
  /**
   * Most recent recorded manual or automatic vacuum.
   */
  last_vacuum_at?: string;
  /**
   * Most recent recorded manual or automatic analyze.
   */
  last_analyze_at?: string;
};

