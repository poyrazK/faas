/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessReminderPolicy } from './MCPReadinessReminderPolicy.js';
/**
 * Immutable attention decision retained transactionally with the incident state change.
 */
export type MCPReadinessAttentionReceipt = {
  /**
   * Provider provenance for this MCPReadinessAttentionReceipt; absence denotes a local account action.
   */
  source?: 'pagerduty';
  /**
   * PagerDuty event associated with this MCPReadinessAttentionReceipt.
   */
  provider_event_id?: string;
  /**
   * Provider incident associated with this MCPReadinessAttentionReceipt.
   */
  provider_incident_id?: string;
  /**
   * Signed provider event time retained in this MCPReadinessAttentionReceipt.
   */
  provider_occurred_at?: string;
  request_id: string;
  incident_id: string;
  action: 'acknowledge' | 'release' | 'configure_reminders';
  reason: string;
  created_by: string;
  created_at: string;
  version: number;
  expires_at?: string;
  reminder_policy?: MCPReadinessReminderPolicy;
};

