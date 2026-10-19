/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessDestinationQueue } from './MCPReadinessDestinationQueue.js';
import type { MCPReadinessIncidentEvent } from './MCPReadinessIncidentEvent.js';
/**
 * One readiness webhook rule and its full outstanding queue independent of incident health.
 */
export type MCPReadinessNotificationQueue = {
  /**
   * Ordered receiver queues including fallback reservations awaiting the primary outcome.
   */
  destinations?: Array<MCPReadinessDestinationQueue>;
  rule_id: string;
  name: string;
  /**
   * Omitted for an all-environment rule.
   */
  mcp_readiness_scope?: string;
  enabled: boolean;
  outstanding_count: number;
  /**
   * Events newer than the oldest event; inspect destinations for actual receiver blocking.
   */
  blocked_count: number;
  oldest_age_seconds: number;
  state: 'idle' | 'ready' | 'delayed' | 'delivering' | 'paused' | 'maintenance';
  /**
   * Safe failure classification without webhook URLs or remote response bodies.
   */
  failure_kind?: 'http_4xx' | 'http_5xx' | 'http_other' | 'transport' | 'lease_expired';
  /**
   * Snapshot hint; the action transaction rechecks current eligibility.
   */
  can_retry: boolean;
  /**
   * Whether the current snapshot permits permanent abandonment.
   */
  can_abandon: boolean;
  head?: MCPReadinessIncidentEvent;
};

