/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowDiagnosticStep } from './WorkflowDiagnosticStep.js';
import type { WorkflowResumePreview } from './WorkflowResumePreview.js';
/**
 * Read-only durable state and recovery preview. No execution or capacity reservation. Steps are bounded by existing workflow and iteration limits.
 */
export type WorkflowRunDiagnosticsResponse = {
  run_id: string;
  workflow_name: string;
  status: 'pending' | 'running' | 'awaiting_event' | 'succeeded' | 'failed' | 'dead';
  observed_at: string;
  /**
   * Original code pin retained across continuation. Omitted for legacy unpinned runs.
   */
  deployment_id?: string;
  legacy_unpinned: boolean;
  /**
   * Durable state or dispatch admission reason at observation. Ready does not imply immediate execution or runtime availability. Inspect step kinds for parked wait details.
   */
  state_reason: 'ready' | 'scheduled' | 'retry_backoff' | 'parked_wait' | 'app_capacity' | 'tenant_capacity' | 'workflow_capacity' | 'running' | 'succeeded' | 'failed' | 'dead' | 'cancelled';
  /**
   * Next intentional wake or scheduling deadline when it is in the future.
   */
  next_wake_at?: string;
  /**
   * Age since eligibility including capacity-blocked work and expired leases; zero for future waits and live claims.
   */
  due_age_seconds: number;
  stale_lease: boolean;
  steps: Array<WorkflowDiagnosticStep>;
  resume: WorkflowResumePreview;
};

