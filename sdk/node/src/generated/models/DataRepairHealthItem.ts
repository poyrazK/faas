/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Operator recovery metadata for an original rehearsal task, apply operation or pending replacement.
 */
export type DataRepairHealthItem = {
  cursor_key: string;
  operation_id: string;
  task_id?: string;
  account_id: string;
  app_id: string;
  node_id?: string;
  phase: 'queued' | 'running' | 'cleanup' | 'publishing' | 'applying' | 'outcome_unknown' | 'replacing' | 'resolving';
  phase_started_at: string;
  age_seconds: number;
  node_status: 'unassigned' | 'available' | 'unavailable' | 'missing' | 'retired' | 'heartbeat_stale';
  capacity_held: boolean;
  held_memory_mb: number;
  held_vcpus: number;
  cancel_requested: boolean;
  failure_code?: string;
  needs_attention: boolean;
  recovery_guidance: string;
  runbook_url: string;
};

