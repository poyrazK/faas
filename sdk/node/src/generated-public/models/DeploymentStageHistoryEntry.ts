/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One closed stage with server-measured duration.
 */
export type DeploymentStageHistoryEntry = {
  name: 'source_download' | 'dependency_restore' | 'image_build' | 'security_scan' | 'snapshot_prepare' | 'readiness';
  started_at: string | null;
  ended_at: string | null;
  duration_ms: number;
  status: 'completed' | 'failed' | 'cancelled';
  /**
   * Failure or cancellation detail; absent or empty for completed stages.
   */
  reason?: string;
};

