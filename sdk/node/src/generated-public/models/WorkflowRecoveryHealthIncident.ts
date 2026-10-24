/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryHealthNotification } from './WorkflowRecoveryHealthNotification.js';
/**
 * Latest retained health incident per app across policy versions. Reason changes do not create repeated alerts. Unknown snapshots defer initial capture and cannot declare recovery. Disabling alerts closes silently; healthy or inactive observations close with recovery only after alert capture. Historical webhook payloads remain immutable.
 */
export type WorkflowRecoveryHealthIncident = {
  episode_id: string;
  opened_at: string;
  candidate_since: string;
  observed_at: string;
  status: 'healthy' | 'inactive' | 'paused' | 'blocked' | 'stalled' | 'unknown';
  reason: string;
  closed_at?: string;
  alert_notification?: WorkflowRecoveryHealthNotification;
  recovery_notification?: WorkflowRecoveryHealthNotification;
};

