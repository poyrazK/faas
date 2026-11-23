/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A scale-to-zero database's suspend timeout. Every wake bills at least this much compute.
 */
export type ManagedPostgresSuspendTimeout = {
  database_id: string;
  /**
   * Seconds of inactivity before the database suspends.
   */
  seconds: number;
  /**
   * True when the database has its own value; false when it follows the platform default.
   */
  custom: boolean;
  /**
   * False while the database is running: it takes the value the next time it suspends.
   */
  applied: boolean;
};

