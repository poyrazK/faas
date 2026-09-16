/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * POST response from /v1/apps/{slug}/debug/requests/{req_id}/replay (ADR-127).
 */
export type DebugReplayResponse = {
  /**
   * Durable invocation ID for polling replay status and comparison results.
   */
  mirror_invocation_id?: string | null;
  status: 'queued' | 'running' | 'completed' | 'failed';
  /**
   * Deployment that served the retained request.
   */
  source_deployment_id?: string | null;
  /**
   * Enabled mirror target selected for this replay.
   */
  mirror_deployment_id?: string | null;
};

