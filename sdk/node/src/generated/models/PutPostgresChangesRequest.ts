/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PostgresChangeFeed } from './PostgresChangeFeed.js';
/**
 * Declares the app's Postgres change capture.
 */
export type PutPostgresChangesRequest = {
  /**
   * A managed database name or ID. Omit for a database you run.
   */
  database?: string;
  /**
   * The app queue binding that receives the changes.
   */
  queue_name: string;
  /**
   * How often the outbox is read; default 5. 300 or more lets an idle managed database suspend.
   */
  poll_interval_seconds?: number;
  feeds: Array<PostgresChangeFeed>;
};

