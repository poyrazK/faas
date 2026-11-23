/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessReminderPolicy } from './MCPReadinessReminderPolicy.js';
/**
 * Versioned reminder policy captured when a new readiness webhook incident opens.
 */
export type MCPReadinessReminderDefaults = {
  /**
   * Zero means no defaults have been configured for this rule.
   */
  version: number;
  reminder_policy: MCPReadinessReminderPolicy;
};

