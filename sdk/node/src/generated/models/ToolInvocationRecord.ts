/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ToolInvocationDelegation } from './ToolInvocationDelegation.js';
import type { ToolInvocationIntent } from './ToolInvocationIntent.js';
import type { ToolInvocationOutcome } from './ToolInvocationOutcome.js';
import type { ToolInvocationTarget } from './ToolInvocationTarget.js';
/**
 * Account-owned single-dispatch fact; cancellation after dispatch does not establish remote rollback or successful cancellation.
 */
export type ToolInvocationRecord = {
  delegation?: ToolInvocationDelegation;
  id: string;
  account_id: string;
  intent: ToolInvocationIntent;
  target: ToolInvocationTarget;
  status: 'queued' | 'running' | 'completed' | 'tool_error' | 'not_started' | 'cancelled' | 'outcome_unknown';
  created_at: string;
  deadline: string;
  dispatched_at?: string;
  cancellation_requested: boolean;
  finished_at?: string;
  outcome?: ToolInvocationOutcome;
};

