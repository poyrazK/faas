/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for POST /v1/deployments/{id}/retry. Identifies the stage the retry should resume from. The closed-6 vocabulary mirrors `state.AllStageNames` (ADR-117); the API rejects unknown values with 400. Empty strings are rejected for the same reason.
 */
export type RetryDeploymentRequest = {
  /**
   * Closed-6 stage vocabulary. `source_download` re-runs the whole pipeline (intentional retry-from-top); any other value resumes from that stage with all prior inputs copied from the source row.
   */
  from_stage: 'source_download' | 'dependency_restore' | 'image_build' | 'security_scan' | 'snapshot_prepare' | 'readiness';
};

