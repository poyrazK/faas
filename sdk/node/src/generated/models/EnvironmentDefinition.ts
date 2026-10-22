/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentRecoveryRetention } from './EnvironmentRecoveryRetention.js';
import type { EnvironmentWorkload } from './EnvironmentWorkload.js';
/**
 * Versioned Git intent; omitted settings are unmanaged and removal requires explicit pruning. The workloads map is required; an explicit empty map represents an empty environment.
 */
export type EnvironmentDefinition = {
  recovery_retention?: EnvironmentRecoveryRetention;
  api_version: 'gregale.dev/environment/v1';
  project: string;
  environment: string;
  /**
   * Reviewed whole-workload retirement when source pruning is enabled. Withdraws this environment after replacement qualification, retains configuration and accepted work, and requires original app identity and fresh qualification for recovery.
   */
  workload_pruning_policy?: 'retain';
  /**
   * Reviewed recovery keyed by declared logical workload name, with its original retained app UUID. Queue recovery continues to require original binding UUIDs separately.
   */
  workload_recoveries?: Record<string, string>;
  /**
   * Explicit reviewed disposition for removed owned queues when pruning is enabled. Retires admission and dispatch while preserving binding IDs, accepted work and delivery receipts. Omit to block queue pruning.
   */
  queue_pruning_policy?: 'retain';
  configuration?: Record<string, any>;
  workloads: Record<string, EnvironmentWorkload>;
};

