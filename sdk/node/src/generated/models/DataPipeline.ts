/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineCapacity } from './DataPipelineCapacity.js';
import type { DataPipelineHealth } from './DataPipelineHealth.js';
/**
 * Managed replication intent with bounded, revision-specific worker observations.
 */
export type DataPipeline = {
  /**
   * Conservative source checkpoint time confirmed applied by the destination replication origin. Historical evidence, not measured time lag.
   */
  applied_through_at?: string;
  health?: DataPipelineHealth;
  capacity?: DataPipelineCapacity;
  /**
   * Intent revision covered by the latest observation.
   */
  observed_revision: number;
  /**
   * True only when the current intent was observed within the last 90 seconds.
   */
  observation_fresh: boolean;
  id: string;
  name: string;
  source_connection_id: string;
  database_id: string;
  tables: Array<string>;
  read_subjects: Array<string>;
  desired_state: 'running' | 'paused' | 'deleted';
  state: 'validating' | 'copying' | 'catching_up' | 'live' | 'paused' | 'blocked' | 'deleting' | 'deleted' | 'resync_required';
  revision: number;
  generation: number;
  reason?: string;
  tables_ready: number;
  retained_wal_bytes?: number;
  observed_at?: string;
  verification: 'unknown' | 'pending' | 'passed' | 'mismatch' | 'inconclusive';
  verified_at?: string;
  created_at: string;
};

