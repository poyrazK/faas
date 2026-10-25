/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowScheduleResponse } from './WorkflowScheduleResponse.js';
/**
 * Deployed workflow schedules with runtime availability and any admission blocker.
 */
export type ListWorkflowSchedulesResponse = {
  runtime_enabled: boolean;
  unavailable_reason?: 'runtime_disabled' | 'account_inactive' | 'plan_not_allowed' | 'maintenance' | 'tenant_required';
  schedules: Array<WorkflowScheduleResponse>;
};

