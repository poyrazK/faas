/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessDeliveryRouting } from './MCPReadinessDeliveryRouting.js';
import type { MCPReadinessDestinationDelivery } from './MCPReadinessDestinationDelivery.js';
import type { MCPReadinessMonitorStatus } from './MCPReadinessMonitorStatus.js';
import type { MCPReadinessNotificationReceipt } from './MCPReadinessNotificationReceipt.js';
/**
 * Readiness incident transition or continuation with current notification status.
 */
export type MCPReadinessIncidentEvent = {
  /**
   * Materialized receiver progress with independent retry and lease times.
   */
  destination_deliveries?: Array<MCPReadinessDestinationDelivery>;
  delivery_routing?: MCPReadinessDeliveryRouting;
  /**
   * Logical event ID.
   */
  id: string;
  incident_id: string;
  /**
   * Stable across retries; also the signed webhook ID.
   */
  event_id: string;
  event: 'alert.fired' | 'alert.resolved';
  rule_id: string;
  /**
   * Empty or omitted selects all environments.
   */
  mcp_readiness_scope?: string;
  scopes: Array<string>;
  /**
   * Logical event time
   */
  occurred_at: string;
  attempted_at?: string;
  /**
   * Current incident state independent of notification success.
   */
  incident_status: 'open' | 'resolved';
  delivery_status: 'pending' | 'delivering' | 'delivered' | 'failed' | 'suppressed' | 'abandoned';
  /**
   * Changes on claims, completions, suppression and operator decisions.
   */
  delivery_version?: number;
  last_operator_action?: MCPReadinessNotificationReceipt;
  attempt_count: number;
  /**
   * Windows responsible for suppressing this notification.
   */
  maintenance_window_ids?: Array<string>;
  suppression_reason?: 'maintenance' | 'maintenance_recovered' | 'acknowledged' | 'incident_resolved' | 'reminders_disabled' | 'reminder_obsolete';
  notification_reason?: 'maintenance_ended' | 'reminder';
  /**
   * Per-incident reminder creation number; omitted for ordinary notifications.
   */
  reminder_number?: number;
  /**
   * Next eligible delivery time. Omitted for all terminal delivery states.
   */
  next_attempt_at?: string;
  last_delivery_id?: string;
  last_status_code?: number;
  subjects: Array<MCPReadinessMonitorStatus>;
};

