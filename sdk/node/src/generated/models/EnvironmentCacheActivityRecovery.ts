/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Scoped feed checkpoint and recovery metadata for cursor_expired. Save restart_cursor before rebuilding a live schedule baseline, then consume events after it. Removed historical events are not recoverable; reconcile overlap by schedule identity.
 */
export type EnvironmentCacheActivityRecovery = {
  app_id: string;
  project_id: string;
  environment_id: string;
  scope: string;
  /**
   * Last pruned sequence. Events strictly after it are retained as of this response.
   */
  oldest_cursor: string;
  /**
   * Observed head before baseline discovery; consume activity after this checkpoint to cover concurrent changes.
   */
  restart_cursor: string;
  retention_seconds: number;
};

