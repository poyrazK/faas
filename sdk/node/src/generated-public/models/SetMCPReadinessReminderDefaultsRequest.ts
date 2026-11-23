/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessReminderPolicy } from './MCPReadinessReminderPolicy.js';
/**
 * Complete replacement of rule reminder defaults with optimistic concurrency control.
 */
export type SetMCPReadinessReminderDefaultsRequest = {
  /**
   * Current rule defaults version; required even when it is zero.
   */
  expected_version: number;
  reminder_policy: MCPReadinessReminderPolicy;
};

