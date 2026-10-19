/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessIncidentEvent } from './MCPReadinessIncidentEvent.js';
/**
 * One ordered receiver lane; waiting_primary reserves order until primary succeeds or fails over.
 */
export type MCPReadinessDestinationQueue = {
  /**
   * Native transport of this queue head; omitted for an idle receiver or legacy inspection.
   */
  provider?: 'webhook' | 'pagerduty';
  destination: 'primary' | 'fallback';
  outstanding_count: number;
  blocked_count: number;
  state: 'idle' | 'ready' | 'delayed' | 'delivering' | 'paused' | 'maintenance' | 'waiting_primary' | 'waiting_receiver';
  failure_kind?: 'http_4xx' | 'http_5xx' | 'http_other' | 'transport' | 'lease_expired';
  can_retry: boolean;
  can_abandon: boolean;
  head?: MCPReadinessIncidentEvent;
};

