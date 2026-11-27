/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Session waiting for a PostgreSQL lock and its observed blockers.
 */
export type ManagedPostgresLockWait = {
  pid: number;
  /**
   * Blocking session process IDs. Zero indicates a prepared transaction.
   */
  blocking_pids: Array<number>;
};

