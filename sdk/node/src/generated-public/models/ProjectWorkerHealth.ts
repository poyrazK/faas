/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectWorkerJobSample } from './ProjectWorkerJobSample.js';
/**
 * Opt-in real production queue delivery verification; absence of traffic never proves health.
 */
export type ProjectWorkerHealth = {
  status: 'insufficient_evidence' | 'healthy' | 'regressed';
  candidate: ProjectWorkerJobSample;
  baseline: ProjectWorkerJobSample;
  pending_jobs: number;
  oldest_pending_age_ms: number;
  baseline_pending_jobs: number;
  baseline_oldest_pending_age_ms: number;
};

