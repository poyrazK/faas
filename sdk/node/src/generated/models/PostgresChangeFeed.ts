/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Allowlisted columns of one table whose row changes are captured.
 */
export type PostgresChangeFeed = {
  /**
   * Appears in each event type, database.<name>.<operation>.
   */
  name: string;
  /**
   * Table name; unqualified names are in the public schema.
   */
  table: string;
  /**
   * The only columns an event carries. The primary key must be listed.
   */
  columns: Array<string>;
  /**
   * Operations to capture; all three when omitted.
   */
  operations?: Array<'insert' | 'update' | 'delete'>;
};

