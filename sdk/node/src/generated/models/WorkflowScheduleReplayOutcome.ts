/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type WorkflowScheduleReplayOutcome = {
  occurrence_id: string;
  platform_tenant_id?: string;
  workflow_name?: string;
  scheduled_for?: string;
  outcome: 'eligible' | 'replayed' | 'already_replayed' | 'occurrence_not_found' | 'not_skipped' | 'history_not_replayable' | 'deployment_changed' | 'definition_changed' | 'schedule_disabled' | 'overlap_active' | 'quota_full' | 'tenant_unavailable' | 'target_unavailable' | 'plan_unavailable';
  replay_run_id?: string;
};

