/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable admission outcome for one due schedule minute, retained independently from the workflow run.
 */
export type WorkflowScheduleOccurrenceResponse = {
  id: string;
  app_id: string;
  platform_tenant_id?: string;
  workflow_name: string;
  deployment_id: string;
  scheduled_for: string;
  evaluated_at: string;
  status: 'started' | 'skipped_overlap' | 'skipped_quota';
  /**
   * Present for started outcomes; retained after run expiry.
   */
  run_id?: string;
  /**
   * Present after a skipped occurrence has been replayed.
   */
  replay_run_id?: string;
  replayed_at?: string;
};

