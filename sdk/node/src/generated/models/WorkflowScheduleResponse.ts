/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current deployed schedule configuration and its latest durable admission outcome.
 */
export type WorkflowScheduleResponse = {
  workflow_name: string;
  deployment_id: string;
  schedule: string;
  timezone: string;
  overlap: 'skip' | 'allow';
  enabled: boolean;
  next_fire_at?: string;
  last_evaluated_at?: string;
  last_scheduled_for?: string;
  last_status?: 'armed' | 'started' | 'skipped_overlap' | 'skipped_quota';
  last_run_id?: string;
};

