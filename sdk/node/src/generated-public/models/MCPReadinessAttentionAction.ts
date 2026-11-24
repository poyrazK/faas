/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessReminderPolicy } from './MCPReadinessReminderPolicy.js';
/**
 * Version-fenced attention decision. Acknowledge accepts optional expires_at; configure_reminders requires reminder_policy; release accepts neither.
 */
export type MCPReadinessAttentionAction = {
  /**
   * Reuse this UUID and identical body when retrying the same decision.
   */
  request_id: string;
  /**
   * Current incident attention version; zero is valid.
   */
  expected_version: number;
  action: 'acknowledge' | 'release' | 'configure_reminders';
  /**
   * Nonblank explanation limited to 500 UTF-8 bytes.
   */
  reason: string;
  /**
   * Optional absolute acknowledgement expiry within the next 30 days.
   */
  expires_at?: string;
  reminder_policy?: MCPReadinessReminderPolicy;
};

