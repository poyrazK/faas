/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryExecutionSummary } from './EventRecoveryExecutionSummary.js';
import type { EventRecoveryRequest } from './EventRecoveryRequest.js';
export type EventRecoveryJob = {
  /**
   * Current admission rate; selection.rate_per_second retains the original requested rate.
   */
  rate_per_second: number;
  /**
   * Start time of the current pause; present only while paused.
   */
  paused_at?: string;
  id: string;
  execution?: EventRecoveryExecutionSummary;
  app_id: string;
  coverage: 'captured_application_recipients' | 'retained_application_executions';
  selection: EventRecoveryRequest;
  state: 'running' | 'paused' | 'completed' | 'cancelled';
  selected_count: number;
  pending_count: number;
  queued_count: number;
  skipped_count: number;
  cancelled_count: number;
  created_at: string;
  updated_at: string;
  expires_at: string;
  completed_at?: string;
};

