/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessAcknowledgement } from './MCPReadinessAcknowledgement.js';
import type { MCPReadinessAttentionReceipt } from './MCPReadinessAttentionReceipt.js';
import type { MCPReadinessPagerDutyAcknowledgementHold } from './MCPReadinessPagerDutyAcknowledgementHold.js';
import type { MCPReadinessReminderPolicy } from './MCPReadinessReminderPolicy.js';
/**
 * Incident acknowledgement and durable reminder schedule. Version changes on decisions and reminder creation, not routine evidence updates.
 */
export type MCPReadinessIncidentAttention = {
  /**
   * Active tracked PagerDuty acknowledgements in primary then fallback order. Every remaining acknowledgement prevents automatic reminder resumption.
   */
  provider_acknowledgements?: Array<MCPReadinessPagerDutyAcknowledgementHold>;
  /**
   * Whether the current incident policy was inherited or explicitly overridden; legacy denotes unavailable pre-upgrade provenance.
   */
  reminder_policy_source?: 'rule_default' | 'incident_override' | 'legacy';
  /**
   * Rule defaults version captured at incident opening; retained after an incident override and absent for older incidents.
   */
  reminder_defaults_version?: number;
  incident_id: string;
  rule_id: string;
  rule_name: string;
  rule_enabled: boolean;
  /**
   * Incident environment; omission covers every environment.
   */
  mcp_readiness_scope?: string;
  opened_at: string;
  resolved_at?: string;
  version: number;
  state: 'unacknowledged' | 'acknowledged' | 'expired' | 'resolved';
  acknowledgement?: MCPReadinessAcknowledgement;
  reminder_policy: MCPReadinessReminderPolicy;
  reminders_created: number;
  /**
   * Earliest eligibility subject to fresh evidence and queue state.
   */
  next_reminder_at?: string;
  last_observed_at?: string;
  /**
   * Latest matching unhealthy evidence; omitted after healthy or coverage-changed observations.
   */
  last_unhealthy_at?: string;
  last_action?: MCPReadinessAttentionReceipt;
};

