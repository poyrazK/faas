/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingCheckFinding } from './BindingCheckFinding.js';
import type { Problem } from './Problem.js';
import type { ProjectRollbackHealth } from './ProjectRollbackHealth.js';
import type { ProjectRollbackLiveTraffic } from './ProjectRollbackLiveTraffic.js';
import type { ProjectRollbackPreCutoverHealth } from './ProjectRollbackPreCutoverHealth.js';
import type { ProjectRollbackTraffic } from './ProjectRollbackTraffic.js';
import type { ProjectRollbackUndoArtifacts } from './ProjectRollbackUndoArtifacts.js';
import type { RollbackCrossedRelease } from './RollbackCrossedRelease.js';
/**
 * Planned or observed rollback for one service and its exact revision pair.
 */
export type ProjectRollbackStep = {
  service: string;
  slug: string;
  status: 'planned' | 'skipped' | 'blocked' | 'in_progress' | 'rolled_back' | 'failed';
  from_revision?: number;
  to_revision?: number;
  reason?: string;
  app_id?: string;
  scope?: string;
  expected_current_deployment_id?: string;
  current_served_at?: string;
  target_deployment_id?: string;
  deployment_id?: string;
  attempt_id?: string;
  rollback_operation_id?: string;
  /**
   * Target preparation state such as warming, checking, ready, blocked, or released.
   */
  preparation_status?: string;
  blockers?: Array<BindingCheckFinding>;
  warnings?: Array<BindingCheckFinding>;
  /**
   * Release runs crossed by this undo step.
   */
  crossed_releases?: Array<RollbackCrossedRelease>;
  undo_artifacts?: ProjectRollbackUndoArtifacts;
  problem?: Problem;
  pre_cutover_health?: ProjectRollbackPreCutoverHealth;
  health?: ProjectRollbackHealth;
  live_traffic?: ProjectRollbackLiveTraffic;
  traffic?: ProjectRollbackTraffic;
};

