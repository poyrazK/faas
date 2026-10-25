/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectReleaseProbeEvidence } from './ProjectReleaseProbeEvidence.js';
/**
 * Durable post-expansion production probe checkpoint. Commands and database contents remain private.
 */
export type ProjectMigrationBaselineVerification = {
  task_id: string;
  release_id: string;
  started_at: string;
  checked_at: string;
  status: 'healthy' | 'failed' | 'unknown';
  evidence: Array<ProjectReleaseProbeEvidence>;
};

