/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PostgresChangeFeed } from './PostgresChangeFeed.js';
/**
 * The app's Postgres change capture and its relay health.
 */
export type PostgresChangesResponse = {
  source_id: string;
  queue_name: string;
  /**
   * The managed database name, when managed.
   */
  database?: string;
  managed: boolean;
  poll_interval_seconds: number;
  feeds: Array<PostgresChangeFeed>;
  enabled: boolean;
  /**
   * Whether Gregale holds the relay login.
   */
  connected: boolean;
  /**
   * The least-privilege login the install creates for delivery.
   */
  relay_role: string;
  relay_status: string;
  last_checked_at?: string;
  pending_events?: number;
  blocked_events?: number;
  oldest_pending_at?: string;
};

