/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectReleaseControl } from './ProjectReleaseControl.js';
import type { ProjectReleaseProbe } from './ProjectReleaseProbe.js';
import type { ProjectReleaseProbeEvidence } from './ProjectReleaseProbeEvidence.js';
import type { ProjectReleaseStabilization } from './ProjectReleaseStabilization.js';
import type { ProjectWorkerHealth } from './ProjectWorkerHealth.js';
/**
 * Snapshotted critical probes and durable post-cutover verification evidence.
 */
export type ProjectReleaseVerification = {
  stabilization?: ProjectReleaseStabilization;
  control?: ProjectReleaseControl;
  /**
   * Currently available guarded controls; an empty list permits inspection only.
   */
  actions?: Array<'abort' | 'recover'>;
  /**
   * Require staged 5 percent, 25 percent and full project promotion.
   */
  progressive?: boolean;
  /**
   * Current progressive stage traffic percentage.
   */
  percent?: 5 | 25 | 100;
  /**
   * Durable progressive observation generation.
   */
  generation?: number;
  worker_health?: ProjectWorkerHealth;
  probes: Array<ProjectReleaseProbe>;
  deadline?: string;
  checked_at?: string;
  healthy_rounds: number;
  regressed_rounds: number;
  evidence?: Array<ProjectReleaseProbeEvidence>;
};

