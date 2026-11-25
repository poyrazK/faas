/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ManagedPostgresLockWait = {
  pid: number;
  /**
   * Blocking session process IDs. Zero indicates a prepared transaction.
   */
  blocking_pids: Array<number>;
};

