/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineCapacity } from './DataPipelineCapacity.js';
import type { DataPipelineEvolution } from './DataPipelineEvolution.js';
import type { DataPipelineFreshness } from './DataPipelineFreshness.js';
import type { DataPipelineHealth } from './DataPipelineHealth.js';
import type { DataPipelineIndexAdvice } from './DataPipelineIndexAdvice.js';
import type { DataPipelineSchemaReview } from './DataPipelineSchemaReview.js';
import type { DataPipelineServing } from './DataPipelineServing.js';
import type { DataPipelineTenantChange } from './DataPipelineTenantChange.js';
import type { DataPipelineWALRetention } from './DataPipelineWALRetention.js';
/**
 * Managed replication intent with bounded, revision-specific worker observations.
 */
export type DataPipeline = {
  schema_review?: DataPipelineSchemaReview;
  evolution?: DataPipelineEvolution;
  /**
   * Current owner-configured checkpoint confirmation target; zero means disabled and enabled targets range from 15 to 86400 seconds.
   */
  freshness_target_seconds?: number;
  freshness?: DataPipelineFreshness;
  index_advice?: DataPipelineIndexAdvice;
  serving?: DataPipelineServing;
  tenant_change?: DataPipelineTenantChange;
  wal_retention?: DataPipelineWALRetention;
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

