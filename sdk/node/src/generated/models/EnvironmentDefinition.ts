/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentWorkload } from './EnvironmentWorkload.js';
/**
 * Versioned Git intent; omitted settings are unmanaged and removal requires explicit pruning. The workloads map is required; an explicit empty map represents an empty environment.
 */
export type EnvironmentDefinition = {
  api_version: 'gregale.dev/environment/v1';
  project: string;
  environment: string;
  /**
   * Explicit reviewed disposition for removed owned queues when pruning is enabled. Retires admission and dispatch while preserving binding IDs, accepted work and delivery receipts. Omit to block queue pruning.
   */
  queue_pruning_policy?: 'retain';
  configuration?: Record<string, any>;
  workloads: Record<string, EnvironmentWorkload>;
};

