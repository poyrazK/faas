/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresLockWait } from './ManagedPostgresLockWait.js';
/**
 * Connection usage covers the primary instance. Transactions and waiting sessions cover the managed database.
 */
export type ManagedPostgresActivitySample = {
  sampled_at: string;
  /**
   * Client sessions across the primary.
   */
  connections: number;
  /**
   * Engine connection slots available to ordinary logins.
   */
  connection_limit: number;
  active_transactions: number;
  oldest_active_transaction_ms: number;
  idle_in_transaction: number;
  oldest_idle_transaction_ms: number;
  lock_waits: number;
  blocked_sessions: Array<ManagedPostgresLockWait>;
};

