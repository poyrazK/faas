/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectPromotionAdmission } from './ProjectPromotionAdmission.js';
import type { ProjectPromotionControl } from './ProjectPromotionControl.js';
import type { ProjectPromotionHealthPolicy } from './ProjectPromotionHealthPolicy.js';
import type { ProjectPromotionMigrationRecovery } from './ProjectPromotionMigrationRecovery.js';
import type { ProjectPromotionWorkerHealth } from './ProjectPromotionWorkerHealth.js';
import type { ProjectReleaseCommand } from './ProjectReleaseCommand.js';
import type { ProjectReleaseProbeEvidence } from './ProjectReleaseProbeEvidence.js';
import type { ProjectReleaseWorker } from './ProjectReleaseWorker.js';
import type { ProjectWorkerHealth } from './ProjectWorkerHealth.js';
/**
 * Durable destination health checkpoint. Includes counts only, without challenge tokens, credentials or response bodies.
 */
export type ProjectPromotionHealth = {
  workers?: Array<ProjectPromotionWorkerHealth>;
  admission?: ProjectPromotionAdmission;
  control?: ProjectPromotionControl;
  worker?: ProjectReleaseWorker;
  worker_health?: ProjectWorkerHealth;
  recovery?: ProjectPromotionMigrationRecovery;
  migration?: ProjectReleaseCommand;
  policy: ProjectPromotionHealthPolicy;
  phase: 'queued' | 'migrating' | 'preflight' | 'cutover' | 'verifying' | 'finalizing' | 'rolling_back' | 'restoring' | 'aborted' | 'succeeded' | 'failed' | 'conflicted' | 'rollback_failed';
  generation: number;
  /**
   * Queue entry time while waiting; reset once to execution start at validated dispatch.
   */
  started_at: string;
  /**
   * Queue wait deadline while queued; a fresh execution deadline after dispatch.
   */
  deadline: string;
  checked_at?: string;
  verdict: 'unknown' | 'healthy' | 'regressed';
  healthy_rounds: number;
  regressed_rounds: number;
  message?: string;
  evidence?: Array<ProjectReleaseProbeEvidence>;
};

